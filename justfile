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

# Capture the TypeScript behavior as goldens under testdata/golden (only until the TS code is removed).
goldens:
    npx tsx scripts/capture-goldens.ts

ts-build:
    npm run build
    chmod +x dist/bin.js

ts-test:
    npx vitest run

ts-lint:
    npx tsc --noEmit
    npx eslint src/
