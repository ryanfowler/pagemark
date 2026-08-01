//! End-to-end extraction benchmarks.
#![allow(missing_docs)]

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use pagemark::{extract, Options};

fn extraction(c: &mut Criterion) {
    let html = include_str!("../testdata/article-large-code.html");
    c.bench_function("large article", |b| {
        b.iter(|| extract(black_box(html), None, black_box(&Options::default())));
    });
}

criterion_group!(benches, extraction);
criterion_main!(benches);
