release:
	git checkout -b release
	git reset --hard main
	make wasm
	git add -f tokenizers.wasm
	git commit -m "add release binary"


wasm:
	cargo build --release
	cp -f target/wasm32-wasip1/release/tokenizers.wasm tokenizers.wasm
	go vet -v

.PHONY: release wasm
