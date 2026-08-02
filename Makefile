wasm:
	cargo build --release
	cp -f target/wasm32-wasip1/release/tokenizers.wasm tokenizers.wasm
	go vet -v
