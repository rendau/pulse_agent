.DEFAULT_GOAL := build

BINARY_NAME = svc
BUILD_PATH = cmd/build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
BUILT_AT ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X github.com/mechta-market/pulse_agent/internal/constant.Version=$(VERSION) \
	-X github.com/mechta-market/pulse_agent/internal/constant.Commit=$(COMMIT) \
	-X github.com/mechta-market/pulse_agent/internal/constant.BuiltAt=$(BUILT_AT)

.SILENT:

build:
	mkdir -p $(BUILD_PATH)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_PATH)/$(BINARY_NAME) cmd/main.go

clean:
	rm -rf $(BUILD_PATH)

lint:
	golangci-lint run

test:
	go test ./...

# эталонные вопросы агенту (прод-бот через ruto), сравнение с evals/baseline.json;
# часть набора: make eval ARGS="-only order-found,cluster-errors"
eval:
	go run ./cmd/eval -baseline evals/baseline.json $(ARGS)
