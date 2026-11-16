.PHONY: test race lint bench run-local cover

test:
	go test ./...

race:
	go test -race ./...

lint:
	go vet ./...
	golangci-lint run

bench:
	go test -run '^$$' -bench . -benchmem ./internal/...

cover:
	go test -cover ./internal/...

run-local:
	go run ./cmd/local
