use crate::{Error, PageType, UrlPolicy};

/// A resource limit: the library default, no limit, or an explicit maximum.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Limit {
    /// Use the documented library default.
    Default,
    /// Disable this configurable limit.
    Unlimited,
    /// Use this exact maximum.
    Max(u64),
}

impl Limit {
    pub(crate) fn resolve(self, default: u64) -> Option<u64> {
        match self {
            Self::Default => Some(default),
            Self::Unlimited => None,
            Self::Max(value) => Some(value),
        }
    }
}

/// Content-selection precision/recall tradeoff.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum SelectionMode {
    /// Default balanced extraction.
    #[default]
    Balanced,
    /// Prefer less auxiliary content.
    Precision,
    /// Prefer recovering more plausible content.
    Recall,
}

/// Extraction configuration.
#[derive(Clone, Debug)]
pub struct Options {
    /// Override automatic page-type detection.
    pub page_type: Option<PageType>,
    /// Selection precision/recall mode.
    pub selection_mode: SelectionMode,
    /// Maximum source size (default 10 MiB).
    pub max_input_bytes: Limit,
    /// Maximum Markdown size (default 2 MiB).
    pub max_output_bytes: Limit,
    /// Keep safe links.
    pub include_links: bool,
    /// Keep useful images.
    pub include_images: bool,
    /// Render supported tables as GFM tables.
    pub include_tables: bool,
    /// Policy for output links and images only.
    pub url_policy: UrlPolicy,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            page_type: None,
            selection_mode: SelectionMode::Balanced,
            max_input_bytes: Limit::Default,
            max_output_bytes: Limit::Default,
            include_links: true,
            include_images: true,
            include_tables: true,
            url_policy: UrlPolicy::default(),
        }
    }
}

impl Options {
    pub(crate) const MAX_INPUT_DEFAULT: u64 = 10 << 20;
    pub(crate) const MAX_OUTPUT_DEFAULT: u64 = 2 << 20;

    pub(crate) fn validate(&self) -> Result<(), Error> {
        if matches!(self.max_output_bytes, Limit::Max(value) if usize::try_from(value).is_err()) {
            return Err(Error::InvalidOption(
                "output byte maximum does not fit this platform".into(),
            ));
        }
        self.url_policy.validate()
    }
}
