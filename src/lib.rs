//! Extract useful content and metadata from untrusted UTF-8 HTML.
//!
//! Pagemark is a pure extraction library: it does not fetch URLs or execute
//! JavaScript. Returned source words remain untrusted even though generated
//! Markdown never contains raw HTML.
#![forbid(unsafe_code)]

mod dom;
mod error;
mod extractor;
mod markdown;
mod metadata;
mod options;
mod types;
mod url_policy;

pub use error::{Error, LimitResource};
pub use options::{Limit, Options, SelectionMode};
pub use types::{Document, Image, Link, PageType, Section};
pub use url_policy::UrlPolicy;

use url::Url;

/// Extracts a document from UTF-8 HTML.
///
/// `page_url`, when present, must be an absolute HTTP(S) URL. Credentials are
/// removed from the URL returned in [`Document`].
///
/// # Errors
///
/// Returns a structured [`Error`] for invalid options or URLs, exceeded
/// resource limits, parse failures, or when no useful content is found.
pub fn extract(html: &str, page_url: Option<&Url>, options: &Options) -> Result<Document, Error> {
    extractor::extract_impl(html, page_url, options)
}

/// Extracts a document from bytes after validating UTF-8 and the input limit.
///
/// # Errors
///
/// Returns [`Error::InvalidUtf8`] for non-UTF-8 input and the same structured
/// errors as [`extract`] for all other failures.
pub fn extract_bytes(
    html: &[u8],
    page_url: Option<&Url>,
    options: &Options,
) -> Result<Document, Error> {
    options.validate()?;
    if let Some(max) = options.max_input_bytes.resolve(Options::MAX_INPUT_DEFAULT) {
        if html.len() as u64 > max {
            return Err(Error::Limit {
                resource: LimitResource::InputBytes,
                count: html.len() as u64,
                max,
            });
        }
    }
    let text = std::str::from_utf8(html).map_err(Error::InvalidUtf8)?;
    extractor::extract_validated(text, page_url, options)
}
