//! Parser resource-bound tests.

use pagemark::{extract, Error, LimitResource, Options};

#[test]
fn element_limit_is_typed() {
    let html = format!("<main>{}</main>", "<i></i>".repeat(200_000));
    let error = extract(&html, None, &Options::default()).unwrap_err();
    assert!(
        matches!(error, Error::Limit { resource: LimitResource::Elements, count, max: 200_000 } if count > 200_000)
    );
}

#[test]
fn depth_limit_is_typed() {
    let html = format!("{}content{}", "<div>".repeat(300), "</div>".repeat(300));
    assert!(matches!(
        extract(&html, None, &Options::default()),
        Err(Error::Limit {
            resource: LimitResource::Depth,
            max: 256,
            ..
        })
    ));
}
