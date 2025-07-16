.PHONY: all lint clean

# 出力ディレクトリ
OUT_DIR := ../dashboards

# jsonnetファイル
JSONNET_FILES := $(wildcard *.jsonnet)

# 出力されるjsonファイル
JSON_FILES := $(patsubst %.jsonnet,$(OUT_DIR)/%.json,$(JSONNET_FILES))

# jsonnet-bundlerのパス
JB := $(GOPATH)/bin/jb

# jsonnetのDockerイメージ
JSONNET_IMAGE := grafana/jsonnet-libs:latest

all: $(JSON_FILES)

# jsonnetをjsonに変換
$(OUT_DIR)/%.json: %.jsonnet
	@echo "Rendering $< to $@"
	@mkdir -p $(OUT_DIR)
	@docker run --rm -v $(PWD):/app -w /app $(JSONNET_IMAGE) /usr/bin/jsonnet \
		-J vendor/ \
		--ext-str grafana_version="9.5.1" \
		-o $@ $<

lint:
	@echo "Linting Jsonnet files..."
	@docker run --rm -v $(PWD):/app -w /app $(JSONNET_IMAGE) /usr/bin/jsonnet-lint \
		-J vendor/ $(JSONNET_FILES)

clean:
	@echo "Cleaning generated files..."
	@rm -f $(JSON_FILES)

install-deps:
	@echo "Installing Jsonnet dependencies..."
	@if [ ! -d "vendor" ]; then \
		$(JB) init; \
		$(JB) install github.com/grafana/grafonnet-lib/grafonnet@main; \
	fi
