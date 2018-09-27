package main // import "go-mail"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/pat"
	mailgun "github.com/mailgun/mailgun-go"
)

type config struct {
	Domain              string   `json:"domain"`
	PrivateAPIKey       string   `json:"privateAPIKey"`
	PublicValidationKey string   `json:"publicValidationKey"`
	ToAddress           string   `json:"toAddress"`
	Referers            []string `json:"referers"`
}

type srv struct {
	c  *config
	mg mailgun.Mailgun
	m  *message
}

func homeHandler(wr http.ResponseWriter, req *http.Request) {
	dump, err := httputil.DumpRequest(req, true)
	if err != nil {
		log.Fatal(err)
	}
	wr.WriteHeader(http.StatusOK)
	fmt.Fprintf(wr, "%q", dump)
}

type message struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Message  string `json:"message"`
	Honeypot string `json:"honeypot"`
}

func (s *srv) mailHandler(w http.ResponseWriter, r *http.Request) {
	if s.c == nil {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, http.StatusText(http.StatusInternalServerError))
		return
	}

	if len(s.c.Referers) != 0 {
		if !containsString(s.c.Referers, r.Referer()) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, http.StatusText(http.StatusForbidden))
			return
		}
	} else {
		log.Println("no referers defined")
	}

	defer r.Body.Close()
	var m message
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		log.Printf("failed to decode message body with error : %v\n", err)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, http.StatusText(http.StatusInternalServerError))
		return
	}

	if m.Honeypot != "" {
		goto StatusOK
	}

	if err := s.sendMessage(m.Email, m.Name, m.Message, s.c.ToAddress); err != nil {
		log.Printf("failed to send email with error: %v\n", err)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, http.StatusText(http.StatusInternalServerError))
		return
	}

StatusOK:
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{\"status\":\"ok\"}"))
}

func (s *srv) loadConfigFromFile(path string) error {
	jsonFile, err := os.Open(path)
	if err != nil {
		s.c = nil
		return err
	}
	defer jsonFile.Close()

	bytes, err := ioutil.ReadAll(jsonFile)
	if err != nil {
		s.c = nil
		return err
	}

	var c config
	json.Unmarshal(bytes, &c)
	s.c = &c

	return nil
}

func (s *srv) loadConfigFromEnv() error {
	c := config{
		Domain:              os.Getenv("MGDOMAIN"),
		PrivateAPIKey:       os.Getenv("MGPRIVATEKEY"),
		PublicValidationKey: os.Getenv("MGPUBLICKEY"),
		ToAddress:           os.Getenv("TOADDRESS"),
		Referers:            strings.Split(os.Getenv("REFERERS"), ","),
	}
	if c.Domain == "" || c.PrivateAPIKey == "" || c.PublicValidationKey == "" || c.ToAddress == "" {
		s.c = nil
		return errors.New("configuration environment variables no set")
	}

	s.c = &c

	return nil
}

func (s *srv) sendMessage(sender, subject, body, recipient string) error {
	message := s.mg.NewMessage(sender, subject, body, recipient)
	resp, id, err := s.mg.Send(message)

	if err != nil {
		return err
	}

	log.Printf("ID: %s Resp: %s\n", id, resp)

	return nil
}

func (s *srv) configureMailgun() {
	mg := mailgun.NewMailgun(s.c.Domain, s.c.PrivateAPIKey, s.c.PublicValidationKey)
	s.mg = mg
}

func containsString(sl []string, v string) bool {
	for _, vv := range sl {
		if vv == v {
			return true
		}
	}
	return false
}

func main() {
	port := os.Getenv("PORT")

	if port == "" {
		log.Fatal("$PORT must be set")
	}

	var s srv
	if err := s.loadConfigFromFile(".config"); err != nil {
		log.Println(err)
		err = nil
		if err = s.loadConfigFromEnv(); err != nil {
			log.Println(err)
		} else {
			s.configureMailgun()
		}
	} else {
		s.configureMailgun()
	}

	router := pat.New()
	router.Post("/mail", s.mailHandler)
	router.Options("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{\"status\":\"ok\"}"))
		return
	})

	httpSrv := &http.Server{
		Addr:         ":" + port,
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      handlers.CombinedLoggingHandler(os.Stdout, router),
	}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil {
			log.Printf("web server didn't start with error: %v", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	httpSrv.Shutdown(ctx)
	log.Println("shutting down")
	os.Exit(0)
}
