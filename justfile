default:
    @just --list

build:
    npm run build
    chmod +x dist/bin.js

dev *args:
    npx tsx src/bin.ts {{args}}

test:
    npx vitest run

test-watch:
    npx vitest

lint:
    npx tsc --noEmit
    npx eslint src/

clean:
    rm -rf dist

goldens:
    npx tsx scripts/capture-goldens.ts
