/// Resource associated with a hard extraction limit.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
#[cfg_attr(feature = "serde", serde(rename_all = "kebab-case"))]
pub enum LimitResource {
    /// Source HTML bytes.
    InputBytes,
    /// Parsed elements.
    Elements,
    /// DOM nesting depth.
    Depth,
}

/// Extraction failure.
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum Error {
    /// No useful output was found.
    #[error("pagemark: no useful content")]
    NoContent,
    /// The supplied page URL is not hierarchical HTTP(S).
    #[error("pagemark: invalid page URL")]
    InvalidPageUrl,
    /// An option is invalid.
    #[error("pagemark: invalid option: {0}")]
    InvalidOption(String),
    /// Byte input is not UTF-8.
    #[error("pagemark: input is not valid UTF-8")]
    InvalidUtf8(#[source] std::str::Utf8Error),
    /// A hard resource limit was exceeded.
    #[error("pagemark: {resource:?} count {count} exceeds maximum {max}")]
    Limit {
        /// Limited resource.
        resource: LimitResource,
        /// Observed amount.
        count: u64,
        /// Configured maximum.
        max: u64,
    },
    /// HTML parsing failed internally.
    #[error("pagemark: parse failure: {0}")]
    Parse(String),
    /// An extraction invariant failed.
    #[error("pagemark: internal extraction failure: {0}")]
    Internal(String),
}
