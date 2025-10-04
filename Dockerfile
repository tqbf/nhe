FROM golang:1.24

RUN apt-get update && apt-get install -y nodejs npm && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY package.json package-lock.json ./
RUN npm install

COPY . .

RUN make build

EXPOSE 8080

CMD ["./nhe", "--db", "app.db", "serve"]
