.PHONY: test race lint bench cover run run-local headless train eval pytest loadtest docker

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

run:
	go run ./cmd/server -fill rl,baseline

run-local:
	go run ./cmd/local

headless:
	go run ./cmd/headless -matches 2000 -bots 'baseline,random*7'

# RL: train against the baseline, then evaluate the exported policy on seeds never used in training.
train:
	cd trainer && uv run python train.py

eval:
	go run ./cmd/headless -matches 2000 -seed 100000 -bots rl,baseline

pytest:
	cd trainer && uv run pytest -q

# Load test: starts a server with raised limits, then steps up the session count.
loadtest:
	go build -o bin/server ./cmd/server && go build -o bin/loadtest ./cmd/loadtest
	ulimit -n 20000; rm -rf /tmp/lps-load; \
	./bin/server -addr 127.0.0.1:2298 -data /tmp/lps-load -loadtest -fill baseline \
		-max-sessions 10000 -max-per-ip 10000 -conn-rate 100000 -max-matches 2000 & \
	sleep 1; for n in 100 200 400 800 1200 1600; do ./bin/loadtest -addr 127.0.0.1:2298 -sessions $$n -duration 30s; sleep 3; done; \
	kill %1

docker:
	docker build -f deploy/Dockerfile -t lastprocess .
