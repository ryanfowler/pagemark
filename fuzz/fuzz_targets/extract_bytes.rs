#![no_main]

use libfuzzer_sys::fuzz_target;
use pagemark::{extract_bytes, Options};

fuzz_target!(|data: &[u8]| {
    let _ = extract_bytes(data, None, &Options::default());
});
