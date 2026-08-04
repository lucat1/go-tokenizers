use once_cell::sync::Lazy;
use std::mem::MaybeUninit;
use std::slice;
use std::sync::{Mutex, OnceLock};
use thiserror::Error;
use tokenizers::Tokenizer;

#[derive(Error, Debug)]
pub enum Error {
    #[error("loading tokenizer: {0}")]
    Load(Box<dyn std::error::Error + Send + Sync>),

    #[error("tokenizer is not initialized")]
    Uninitialized,

    #[error("encoding into tokens: {0}")]
    Encode(Box<dyn std::error::Error + Send + Sync>),
}

/// WebAssembly export that allocates a pointer (linear memory offset) that can
/// be used for a string.
///
/// This is an ownership transfer, which means the caller must call
/// [`deallocate`] when finished.
#[cfg_attr(all(target_arch = "wasm32"), unsafe(export_name = "allocate"))]
pub extern "C" fn _allocate(size: u32) -> *mut u8 {
    allocate(size as usize)
}

/// Allocates size bytes and leaks the pointer where they start.
fn allocate(size: usize) -> *mut u8 {
    // Allocate the amount of bytes needed.
    let vec: Vec<MaybeUninit<u8>> = vec![MaybeUninit::uninit(); size];

    // into_raw leaks the memory to the caller.
    Box::into_raw(vec.into_boxed_slice()) as *mut u8
}

/// WebAssembly export that deallocates a pointer of the given size (linear
/// memory offset, byteCount) allocated by [`allocate`].
#[cfg_attr(all(target_arch = "wasm32"), unsafe(export_name = "deallocate"))]
pub unsafe extern "C" fn _deallocate(ptr: u32, size: u32) {
    unsafe { deallocate(ptr as *mut u8, size as usize) };
}

/// Retakes the pointer which allows its memory to be freed.
unsafe fn deallocate(ptr: *mut u8, size: usize) {
    let _ = unsafe { Vec::from_raw_parts(ptr, 0, size) };
}

static LAST_ERROR: Lazy<Mutex<String>> = Lazy::new(|| Mutex::new(String::new()));

enum Return {
    Err(Error),
    Data(Box<[u32]>),
    None,
}

impl Return {
    fn pack(self) -> u64 {
        let (is_error, ptr, size) = match self {
            Return::Err(s) => {
                *LAST_ERROR.lock().unwrap() = s.to_string();
                let s = LAST_ERROR.lock().unwrap();
                let encoding = (true, s.as_ptr() as u32, s.len() as u32);
                encoding
            }
            Return::Data(v) => {
                let encoding = (false, v.as_ptr() as u32, v.len() as u32);
                std::mem::forget(v);
                encoding
            }
            Return::None => (false, 0, 0),
        };

        debug_assert!(size <= 0x7fff_ffff); // must fit in 31 bits
        ((is_error as u64) << 63) | ((ptr as u64) << 31) | ((size & 0x7fff_ffff) as u64)
    }
}

impl From<Result<(), Error>> for Return {
    fn from(value: Result<(), Error>) -> Self {
        match value {
            Ok(_) => Return::None,
            Err(err) => Return::Err(err),
        }
    }
}

impl From<Result<Box<[u32]>, Error>> for Return {
    fn from(value: Result<Box<[u32]>, Error>) -> Self {
        match value {
            Ok(vec) => Return::Data(vec),
            Err(err) => Return::Err(err),
        }
    }
}

static TOKENIZER: OnceLock<Tokenizer> = OnceLock::new();

#[cfg_attr(all(target_arch = "wasm32"), unsafe(export_name = "load_tokenizer"))]
pub extern "C" fn _load_tokenizer(ptr: *const u8, len: usize) -> u64 {
    let bytes = unsafe { slice::from_raw_parts(ptr, len) };
    let ret: Return = load_tokenizer(bytes).into();
    ret.pack()
}

fn load_tokenizer(bytes: &[u8]) -> Result<(), Error> {
    let tokenizer = Tokenizer::from_bytes(bytes).map_err(Error::Load)?;
    TOKENIZER.get_or_init(|| tokenizer);
    Ok(())
}

unsafe fn utf8_from_ptr<'a>(ptr: *const u8, len: usize) -> &'a str {
    unsafe { std::str::from_utf8_unchecked(slice::from_raw_parts(ptr, len)) }
}

#[cfg_attr(all(target_arch = "wasm32"), unsafe(export_name = "encode"))]
pub extern "C" fn _encode(ptr: *const u8, len: usize) -> u64 {
    let text = unsafe { utf8_from_ptr(ptr, len) };
    let ret: Return = encode(text).into();
    ret.pack()
}

fn encode(text: &str) -> Result<Box<[u32]>, Error> {
    let tokenizer = TOKENIZER.get().ok_or(Error::Uninitialized)?;
    let encoding = tokenizer.encode(text, true).map_err(Error::Encode)?;

    Ok(encoding.get_ids().to_vec().into_boxed_slice())
}
