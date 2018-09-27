FROM golang:1.11-stretch

WORKDIR /code

COPY . .

RUN ["go", "get", "github.com/githubnemo/CompileDaemon"]


ENTRYPOINT CompileDaemon -log-prefix=false -color=true -graceful-kill=true -build="go build -o go-mail ./" -command="./go-mail"
