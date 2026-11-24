.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin examples tests)"
	go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# The shared-labels example has findings on purpose, so exit 1 is expected.
# The healthy example must exit 0. The binary is built first because go run
# turns every failing exit code into 1.
demo:
	go build -o overselect ./plugin/scripts
	./overselect examples/shared-labels || test $$? -eq 1
	./overselect examples/healthy
	./overselect examples/broken || test $$? -eq 3
