BINARY := tgvault

# whisper.cpp вшивается в бинарь через cgo (см. internal/transcribe/native.go).
WHISPER_DIR    := third_party/whisper.cpp
WHISPER_BUILD  := $(WHISPER_DIR)/build_go
# GGML_NATIVE=ON — быстрее на машине сборки; для релизных бинарей ставь OFF
# (переносимость между CPU): make whisper-libs WHISPER_GGML_NATIVE=OFF
WHISPER_GGML_NATIVE ?= ON

.PHONY: build whisper-libs run test vet tidy clean

# Собрать статические библиотеки whisper.cpp под текущую систему.
whisper-libs:
	@if [ ! -d "$(WHISPER_DIR)" ]; then \
		git clone --depth 1 https://github.com/ggml-org/whisper.cpp "$(WHISPER_DIR)"; \
	fi
	cmake -S $(WHISPER_DIR) -B $(WHISPER_BUILD) \
		-DCMAKE_BUILD_TYPE=Release \
		-DBUILD_SHARED_LIBS=OFF \
		-DGGML_NATIVE=$(WHISPER_GGML_NATIVE) \
		-DWHISPER_BUILD_TESTS=OFF \
		-DWHISPER_BUILD_EXAMPLES=OFF
	cmake --build $(WHISPER_BUILD) --target whisper -j

build: whisper-libs
	go build -o $(BINARY) ./cmd/tgvault

run: build
	./$(BINARY)

vet: whisper-libs
	go vet ./...

tidy:
	go mod tidy

test: whisper-libs
	go test ./...

clean:
	rm -rf dist $(BINARY) $(WHISPER_BUILD)
