use url::Url;

use crate::Error;

/// Safety policy for links and images emitted in content.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct UrlPolicy {
    /// Allowed schemes, compared using ASCII case folding.
    pub allowed_schemes: Vec<String>,
    /// Maximum source URL length. `None` is unlimited.
    pub max_length: Option<usize>,
    /// Remove `utm_*`, `fbclid`, and `gclid` query parameters.
    pub strip_tracking: bool,
}

impl Default for UrlPolicy {
    fn default() -> Self {
        Self {
            allowed_schemes: vec!["http".into(), "https".into()],
            max_length: Some(4096),
            strip_tracking: false,
        }
    }
}

impl UrlPolicy {
    pub(crate) fn validate(&self) -> Result<(), Error> {
        for raw in &self.allowed_schemes {
            let mut chars = raw.chars();
            let valid = chars.next().is_some_and(|c| c.is_ascii_alphabetic())
                && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '-' | '.'));
            if !valid {
                return Err(Error::InvalidOption(format!("invalid URL scheme {raw:?}")));
            }
        }
        Ok(())
    }

    pub(crate) fn resolve(&self, raw: &str, base: Option<&Url>) -> Option<Url> {
        if raw.chars().any(char::is_control) || raw.trim().is_empty() {
            return None;
        }
        if self.max_length.is_some_and(|max| raw.len() > max) {
            return None;
        }
        let raw = raw.trim();
        let mut url = match base {
            Some(base) => base.join(raw).ok()?,
            None => Url::parse(raw).ok()?,
        };
        if !self
            .allowed_schemes
            .iter()
            .any(|s| s.eq_ignore_ascii_case(url.scheme()))
        {
            return None;
        }
        if !url.username().is_empty() || url.password().is_some() {
            return None;
        }
        if matches!(url.scheme(), "http" | "https") && url.host_str().is_none() {
            return None;
        }
        if self.strip_tracking {
            if let Some(query) = url.query().map(strip_tracking_query) {
                if query.is_empty() {
                    url.set_query(None);
                } else {
                    url.set_query(Some(&query));
                }
            }
        }
        Some(url)
    }
}

fn strip_tracking_query(query: &str) -> String {
    query
        .split('&')
        .filter(|component| {
            let key = component.split_once('=').map_or(*component, |(key, _)| key);
            let decoded = percent_decode(key)
                .unwrap_or_else(|| key.to_owned())
                .to_ascii_lowercase();
            !decoded.starts_with("utm_") && decoded != "fbclid" && decoded != "gclid"
        })
        .collect::<Vec<_>>()
        .join("&")
}

fn percent_decode(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut output = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'%' if index + 2 < bytes.len() => {
                let high = (bytes[index + 1] as char).to_digit(16)?;
                let low = (bytes[index + 2] as char).to_digit(16)?;
                output.push(u8::try_from(high * 16 + low).ok()?);
                index += 3;
            }
            b'%' => return None,
            b'+' => {
                output.push(b' ');
                index += 1;
            }
            byte => {
                output.push(byte);
                index += 1;
            }
        }
    }
    String::from_utf8(output).ok()
}

pub(crate) fn sanitized_page_url(input: &Url) -> Option<Url> {
    if !matches!(input.scheme(), "http" | "https")
        || input.host_str().is_none()
        || input.cannot_be_a_base()
    {
        return None;
    }
    let mut result = input.clone();
    let _ = result.set_username("");
    let _ = result.set_password(None);
    Some(result)
}

pub(crate) fn canonical(raw: &str, base: Option<&Url>) -> Option<Url> {
    let mut value = match base {
        Some(base) => base.join(raw.trim()).ok()?,
        None => Url::parse(raw.trim()).ok()?,
    };
    if !matches!(value.scheme(), "http" | "https")
        || value.host_str().is_none()
        || value.cannot_be_a_base()
    {
        return None;
    }
    let _ = value.set_username("");
    let _ = value.set_password(None);
    Some(value)
}
