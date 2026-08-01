//! Basic extraction example.

use pagemark::{extract, Options};
use url::Url;

fn main() -> Result<(), pagemark::Error> {
    let html = "<main><h1>Guide</h1><p>Install the tool.</p></main>";
    let page = Url::parse("https://example.com/guide").expect("static URL is valid");
    let document = extract(html, Some(&page), &Options::default())?;
    println!("{}", document.title.as_deref().unwrap_or("Untitled"));
    println!("{}", document.markdown);
    Ok(())
}
