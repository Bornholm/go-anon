FROM golang:1.25-alpine AS build

WORKDIR /src

ENV CGO_ENABLED=0
COPY go.* .

RUN go mod download

COPY . .

RUN go build -o bin/server ./cmd/server

FROM alpine:3.23 AS runtime

COPY --from=build /src/bin/server /app/bin/server

EXPOSE 8080

CMD ["/app/bin/server", "-models", "fr:/app/models/fr.crf.gz,es:/app/models/es.crf.gz,en:/app/models/en.crf.gz", "-gazetteers", "firstnames:/app/models/gazetteers/eu_prenoms.txt,locations:/app/models/gazetteers/fr_communes.txt"]