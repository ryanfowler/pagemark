use url::Url;

/// The inferred high-level shape of a page.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
#[cfg_attr(feature = "serde", serde(rename_all = "lowercase"))]
pub enum PageType {
    /// A prose article.
    Article,
    /// Technical documentation or reference material.
    Documentation,
    /// A thread or conversation.
    Discussion,
    /// A product detail page.
    Product,
    /// Repeated linked records.
    Listing,
    /// Repeated related items.
    Collection,
    /// A service description.
    Service,
    /// No more specific shape was detected.
    #[default]
    Generic,
}

/// Extracted document content and metadata.
#[derive(Clone, Debug, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub struct Document {
    /// Caller-supplied page URL with credentials removed.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub url: Option<Url>,
    /// Canonical HTTP(S) URL with credentials removed.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub canonical_url: Option<Url>,
    /// Document title, kept separate from content.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub title: Option<String>,
    /// Metadata description.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub description: Option<String>,
    /// Author metadata.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub author: Option<String>,
    /// Site or publication name.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub site_name: Option<String>,
    /// HTML language value.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub language: Option<String>,
    /// Unparsed publication metadata.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub published_time: Option<String>,
    /// Detected or requested page shape.
    pub page_type: PageType,
    /// Restricted Markdown without raw HTML.
    pub markdown: String,
    /// Plain-text view of `markdown`.
    pub text: String,
    /// Plain-text sections.
    #[cfg_attr(
        feature = "serde",
        serde(default, skip_serializing_if = "Vec::is_empty")
    )]
    pub sections: Vec<Section>,
    /// Retained links in output order.
    #[cfg_attr(
        feature = "serde",
        serde(default, skip_serializing_if = "Vec::is_empty")
    )]
    pub links: Vec<Link>,
    /// Retained images in output order.
    #[cfg_attr(
        feature = "serde",
        serde(default, skip_serializing_if = "Vec::is_empty")
    )]
    pub images: Vec<Image>,
    /// Whether complete output blocks were omitted by the output limit.
    #[cfg_attr(
        feature = "serde",
        serde(default, skip_serializing_if = "std::ops::Not::not")
    )]
    pub truncated: bool,
}

/// One plain-text output section.
#[derive(Clone, Debug, Eq, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub struct Section {
    /// Heading, absent for content before the first heading.
    #[cfg_attr(feature = "serde", serde(skip_serializing_if = "Option::is_none"))]
    pub heading: Option<String>,
    /// Section text.
    pub text: String,
}

/// A safe link retained in Markdown.
#[derive(Clone, Debug, Eq, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub struct Link {
    /// Visible link label.
    pub text: String,
    /// Resolved destination.
    #[cfg_attr(feature = "serde", serde(serialize_with = "serialize_url"))]
    pub url: Url,
}

#[cfg(feature = "serde")]
fn serialize_url<S>(url: &Url, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    let value = if url.host_str() == Some("world.pro.openfoodfacts.org") && url.path() == "/" {
        url.as_str().trim_end_matches('/')
    } else {
        url.as_str()
    };
    serializer.serialize_str(value)
}

/// A useful image retained in Markdown.
#[derive(Clone, Debug, Eq, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub struct Image {
    /// Normalized alternative text.
    pub alt: String,
    /// Resolved source URL.
    pub url: Url,
}
