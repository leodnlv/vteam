default:
    @just --list

build:
    go build -o dist/vteam ./cmd/vteam

dev *args:
    go run ./cmd/vteam {{args}}

test:
    go test -race ./...

lint:
    go vet ./...
    go tool golangci-lint run ./...

cover:
    go test -coverprofile=dist/coverage.out ./...
    go tool cover -func=dist/coverage.out | tail -1

clean:
    rm -rf dist
