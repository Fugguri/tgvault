BINARY := tgvault

# whisper.cpp вшивается в бинарь через cgo (см. internal/transcribe/native.go).
WHISPER_DIR    := third_party/whisper.cpp
WHISPER_BUILD  := $(WHISPER_DIR)/build_go
# GGML_NATIVE=ON — быстрее на машине сборки; для релизных бинарей ставь OFF
# (переносимость между CPU): make whisper-libs WHISPER_GGML_NATIVE=OFF
WHISPER_GGML_NATIVE ?= ON

.PHONY: build whisper-libs run test vet golden golden-update tidy clean

# Собрать статические библиотеки whisper.cpp под текущую систему.
# Наличие исходников проверяем по CMakeLists.txt, а не по каталогу: каталог
# может появиться сам — например, от распаковки кэша сборки в CI, — и тогда
# клон молча пропускался, а cmake падал с невнятным «does not appear to
# contain CMakeLists.txt».
whisper-libs:
	@if [ ! -d "$(WHISPER_DIR)" ]; then \
		git clone --depth 1 https://github.com/ggml-org/whisper.cpp "$(WHISPER_DIR)"; \
	elif [ ! -f "$(WHISPER_DIR)/CMakeLists.txt" ]; then \
		echo "ошибка: $(WHISPER_DIR) есть, но исходников whisper.cpp в нём нет."; \
		echo "  так выглядит неполный клон или каталог, созданный распаковкой кэша."; \
		echo "  убери каталог и повтори: rm -rf $(WHISPER_DIR)"; \
		exit 1; \
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

# Сверка вывода CLI с эталоном (cmd/tgvault/testdata/cli_golden.txt).
# Правил поведение команд намеренно — обнови эталон и посмотри диф глазами.
golden: whisper-libs
	go test ./cmd/tgvault -run TestCLIGolden

golden-update: whisper-libs
	UPDATE_GOLDEN=1 go test ./cmd/tgvault -run TestCLIGolden -v

clean:
	rm -rf dist $(BINARY) $(WHISPER_BUILD)
