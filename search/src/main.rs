//! drivesearch: the tantivy search sidecar of the streamuploader Drive.
//!
//! The Go server starts this binary as a child process and talks to it over
//! stdin/stdout, one JSON object per line. The sidecar owns one local index
//! directory and never touches object storage; the Go side decides what to
//! index and synchronizes the directory. See `.knowledge/concepts/system/search-sidecar.yaml`.
//!
//! Request:  {"id": 1, "op": "search", ...}
//! Response: {"id": 1, "ok": true, ...} or {"id": 1, "ok": false, "error": "..."}

use std::collections::BTreeMap;
use std::io::{self, BufRead, Write};
use std::path::{Path, PathBuf};

use anyhow::{anyhow, bail, Context, Result};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tantivy::collector::{Count, FacetCollector, TopDocs};
use tantivy::directory::MmapDirectory;
use tantivy::query::{AllQuery, BooleanQuery, Occur, Query, QueryParser, TermQuery};
use tantivy::schema::{
    Facet, FacetOptions, Field, IndexRecordOption, Schema, TextFieldIndexing, TextOptions, Value as _,
    FAST, INDEXED, STORED, STRING,
};
use tantivy::snippet::SnippetGenerator;
use tantivy::tokenizer::{LowerCaser, NgramTokenizer, TextAnalyzer};
use tantivy::{DateTime, Index, IndexReader, IndexWriter, Order, ReloadPolicy, TantivyDocument, Term};

/// Bump when the schema changes; the Go side rebuilds the directory on mismatch.
const SCHEMA_VERSION: &str = "1";
const WRITER_HEAP_BYTES: usize = 64 * 1024 * 1024;
const JA_TOKENIZER: &str = "lang_ja";
const NGRAM_TOKENIZER: &str = "ngram23";

#[derive(Clone, Copy)]
struct Fields {
    id: Field,
    kind: Field,
    tenant: Field,
    file_id: Field,
    name: Field,
    name_ngram: Field,
    name_sort: Field,
    ext: Field,
    size: Field,
    created: Field,
    modified: Field,
    uploaded: Field,
    author: Field,
    facets: Field,
    facets_exact: Field,
    lat: Field,
    lon: Field,
    body: Field,
    text_stored: Field,
    page: Field,
    view: Field,
}

fn build_schema() -> (Schema, Fields) {
    let mut b = Schema::builder();
    let ja = TextOptions::default().set_indexing_options(
        TextFieldIndexing::default()
            .set_tokenizer(JA_TOKENIZER)
            .set_index_option(IndexRecordOption::WithFreqsAndPositions),
    );
    let ngram = TextOptions::default().set_indexing_options(
        TextFieldIndexing::default()
            .set_tokenizer(NGRAM_TOKENIZER)
            .set_index_option(IndexRecordOption::WithFreqsAndPositions),
    );
    let fields = Fields {
        id: b.add_text_field("id", STRING | STORED),
        kind: b.add_text_field("kind", STRING),
        tenant: b.add_text_field("tenant", STRING),
        file_id: b.add_text_field("file_id", STRING | STORED),
        name: b.add_text_field("name", ja.clone() | STORED),
        name_ngram: b.add_text_field("name_ngram", ngram),
        name_sort: b.add_text_field("name_sort", STRING | FAST),
        ext: b.add_text_field("ext", STRING),
        size: b.add_u64_field("size", FAST | STORED),
        created: b.add_date_field("created", FAST | STORED),
        modified: b.add_date_field("modified", FAST | STORED),
        uploaded: b.add_date_field("uploaded", FAST | STORED),
        author: b.add_text_field("author", ja.clone() | STORED),
        facets: b.add_facet_field("facets", FacetOptions::default()),
        facets_exact: b.add_text_field("facets_exact", STRING),
        lat: b.add_f64_field("lat", FAST | STORED),
        lon: b.add_f64_field("lon", FAST | STORED),
        body: b.add_text_field("body", ja),
        text_stored: b.add_text_field("text_stored", STORED),
        page: b.add_u64_field("page", FAST | STORED | INDEXED),
        view: b.add_text_field("view", STRING | STORED),
    };
    (b.build(), fields)
}

#[derive(Clone, Copy, PartialEq)]
enum Tokenizer {
    Lindera,
    Ngram,
}

fn register_tokenizers(index: &Index, tokenizer: Tokenizer) -> Result<()> {
    let ngram = TextAnalyzer::builder(NgramTokenizer::new(2, 3, false)?)
        .filter(LowerCaser)
        .build();
    index.tokenizers().register(NGRAM_TOKENIZER, ngram);
    match tokenizer {
        Tokenizer::Lindera => {
            use lindera::dictionary::{load_embedded_dictionary, DictionaryKind};
            use lindera::mode::Mode;
            use lindera::segmenter::Segmenter;
            use lindera_tantivy::tokenizer::LinderaTokenizer;
            let dictionary = load_embedded_dictionary(DictionaryKind::IPADIC)
                .map_err(|e| anyhow!("load ipadic: {e}"))?;
            let segmenter = Segmenter::new(Mode::Normal, dictionary, None);
            let ja = TextAnalyzer::builder(LinderaTokenizer::from_segmenter(segmenter))
                .filter(LowerCaser)
                .build();
            index.tokenizers().register(JA_TOKENIZER, ja);
        }
        Tokenizer::Ngram => {
            let ja = TextAnalyzer::builder(NgramTokenizer::new(2, 2, false)?)
                .filter(LowerCaser)
                .build();
            index.tokenizers().register(JA_TOKENIZER, ja);
        }
    }
    Ok(())
}

struct Engine {
    index: Index,
    reader: IndexReader,
    writer: IndexWriter,
    f: Fields,
    dir: PathBuf,
}

impl Engine {
    fn open(dir: &Path, tokenizer: Tokenizer) -> Result<Engine> {
        std::fs::create_dir_all(dir).with_context(|| format!("create {}", dir.display()))?;
        let version_file = dir.join("DRIVESEARCH_SCHEMA");
        if version_file.exists() {
            let found = std::fs::read_to_string(&version_file)?;
            if found.trim() != SCHEMA_VERSION {
                bail!("schema_mismatch: index has schema {} but this binary expects {}", found.trim(), SCHEMA_VERSION);
            }
        }
        let (schema, f) = build_schema();
        let index = Index::open_or_create(MmapDirectory::open(dir)?, schema)?;
        register_tokenizers(&index, tokenizer)?;
        std::fs::write(&version_file, SCHEMA_VERSION)?;
        let reader = index
            .reader_builder()
            .reload_policy(ReloadPolicy::Manual)
            .try_into()?;
        let writer = index.writer::<TantivyDocument>(WRITER_HEAP_BYTES)?;
        Ok(Engine { index, reader, writer, f, dir: dir.to_path_buf() })
    }

    fn handle(&mut self, req: &Request) -> Result<Value> {
        match req.op.as_str() {
            "ping" => Ok(json!({"schema_version": SCHEMA_VERSION, "dir": self.dir.display().to_string()})),
            "upsert" => self.upsert(&req.docs),
            "delete" => self.delete(&req.file_ids),
            "commit" => {
                self.writer.commit()?;
                self.reader.reload()?;
                Ok(json!({"num_docs": self.reader.searcher().num_docs()}))
            }
            "reload" => {
                self.reader.reload()?;
                Ok(json!({"num_docs": self.reader.searcher().num_docs()}))
            }
            "stats" => {
                let searcher = self.reader.searcher();
                Ok(json!({
                    "num_docs": searcher.num_docs(),
                    "segments": searcher.segment_readers().len(),
                    "schema_version": SCHEMA_VERSION,
                }))
            }
            "search" => self.search(req),
            "facets" => self.facets(req),
            "best_page" => self.best_page(req),
            other => bail!("unknown op {other}"),
        }
    }

    fn upsert(&mut self, docs: &[Doc]) -> Result<Value> {
        // A file's documents are replaced as a group; page documents are only
        // written together with their file document, so deleting by file_id
        // before adding keeps the index free of stale pages.
        let mut seen: Vec<&str> = Vec::new();
        for d in docs {
            if d.kind == "file" && !seen.contains(&d.file_id.as_str()) {
                self.writer.delete_term(Term::from_field_text(self.f.file_id, &d.file_id));
                seen.push(&d.file_id);
            }
        }
        let mut n = 0usize;
        for d in docs {
            self.writer.add_document(self.to_document(d)?)?;
            n += 1;
        }
        Ok(json!({"added": n}))
    }

    fn delete(&mut self, file_ids: &[String]) -> Result<Value> {
        for id in file_ids {
            self.writer.delete_term(Term::from_field_text(self.f.file_id, id));
        }
        Ok(json!({"deleted_file_ids": file_ids.len()}))
    }

    fn to_document(&self, d: &Doc) -> Result<TantivyDocument> {
        let f = self.f;
        let mut doc = TantivyDocument::new();
        doc.add_text(f.id, &d.id);
        doc.add_text(f.kind, &d.kind);
        doc.add_text(f.tenant, &d.tenant);
        doc.add_text(f.file_id, &d.file_id);
        if let Some(name) = &d.name {
            doc.add_text(f.name, name);
            doc.add_text(f.name_ngram, name);
            doc.add_text(f.name_sort, sort_key(name));
        }
        if let Some(ext) = &d.ext {
            doc.add_text(f.ext, ext.to_lowercase());
        }
        if let Some(size) = d.size {
            doc.add_u64(f.size, size);
        }
        for (field, value) in [(f.created, &d.created), (f.modified, &d.modified), (f.uploaded, &d.uploaded)] {
            if let Some(ts) = value {
                doc.add_date(field, DateTime::from_timestamp_secs(*ts));
            }
        }
        if let Some(author) = &d.author {
            doc.add_text(f.author, author);
        }
        for path in &d.facets {
            let facet = Facet::from_text(path).map_err(|e| anyhow!("facet {path}: {e:?}"))?;
            doc.add_facet(f.facets, facet);
            doc.add_text(f.facets_exact, path);
        }
        if let (Some(lat), Some(lon)) = (d.lat, d.lon) {
            doc.add_f64(f.lat, lat);
            doc.add_f64(f.lon, lon);
        }
        if let Some(body) = &d.body {
            doc.add_text(f.body, body);
            if d.kind == "page" {
                doc.add_text(f.text_stored, body);
            }
        }
        if let Some(page) = d.page {
            doc.add_u64(f.page, page);
        }
        if let Some(view) = &d.view {
            doc.add_text(f.view, view);
        }
        Ok(doc)
    }

    fn query_parser(&self, fields: &[(Field, f32)]) -> QueryParser {
        let mut parser = QueryParser::for_index(&self.index, fields.iter().map(|(f, _)| *f).collect());
        parser.set_conjunction_by_default();
        for (field, boost) in fields {
            parser.set_field_boost(*field, *boost);
        }
        parser
    }

    /// The user's free text over the fields that describe a file.
    fn user_query(&self, q: &str, body_only: bool) -> Option<Box<dyn Query>> {
        let q = q.trim();
        if q.is_empty() {
            return None;
        }
        let f = self.f;
        let fields: Vec<(Field, f32)> = if body_only {
            vec![(f.body, 1.0)]
        } else {
            vec![(f.name, 3.0), (f.name_ngram, 1.0), (f.author, 2.0), (f.body, 1.0)]
        };
        let (query, _errors) = self.query_parser(&fields).parse_query_lenient(q);
        Some(query)
    }

    fn filters(&self, req: &Request, kind: &str) -> Vec<(Occur, Box<dyn Query>)> {
        let f = self.f;
        let mut clauses: Vec<(Occur, Box<dyn Query>)> = vec![(
            Occur::Must,
            Box::new(TermQuery::new(Term::from_field_text(f.kind, kind), IndexRecordOption::Basic)),
        )];
        if let Some(tenant) = &req.tenant {
            clauses.push((Occur::Must, Box::new(TermQuery::new(Term::from_field_text(f.tenant, tenant), IndexRecordOption::Basic))));
        }
        if let Some(file_id) = &req.file_id {
            clauses.push((Occur::Must, Box::new(TermQuery::new(Term::from_field_text(f.file_id, file_id), IndexRecordOption::Basic))));
        }
        for path in &req.facets_any {
            // Exact membership lists direct children of a folder; the prefix
            // form includes everything below the path.
            let term = if req.facet_exact {
                Term::from_field_text(f.facets_exact, path)
            } else {
                Term::from_facet(f.facets, &Facet::from(path.as_str()))
            };
            clauses.push((Occur::Must, Box::new(TermQuery::new(term, IndexRecordOption::Basic))));
        }
        clauses
    }

    fn search(&self, req: &Request) -> Result<Value> {
        let f = self.f;
        let searcher = self.reader.searcher();
        let user = self.user_query(req.q.as_deref().unwrap_or(""), false);
        let mut clauses = self.filters(req, "file");
        if let Some(uq) = &user {
            clauses.push((Occur::Must, uq.box_clone()));
        }
        let query: Box<dyn Query> = if clauses.is_empty() { Box::new(AllQuery) } else { Box::new(BooleanQuery::new(clauses)) };
        let limit = req.limit.unwrap_or(50).clamp(1, 1000);
        let offset = req.offset.unwrap_or(0);
        let sort = req.sort.clone().unwrap_or_else(|| if user.is_some() { "score".into() } else { "name".into() });
        let (desc, key) = match sort.strip_prefix('-') {
            Some(k) => (true, k.to_string()),
            None => (false, sort.clone()),
        };
        let order = if desc { Order::Desc } else { Order::Asc };
        let top = TopDocs::with_limit(limit).and_offset(offset);
        let (addresses, total): (Vec<(f32, tantivy::DocAddress)>, usize) = match key.as_str() {
            "score" => {
                let (hits, total) = searcher.search(&query, &(top.order_by_score(), Count))?;
                (hits, total)
            }
            "name" => {
                let (hits, total) = searcher.search(&query, &(top.order_by_string_fast_field("name_sort", order), Count))?;
                (hits.into_iter().map(|(_, a)| (0.0, a)).collect(), total)
            }
            "modified" | "created" | "uploaded" => {
                let order = if req.sort.is_none() { Order::Desc } else { order };
                let (hits, total) = searcher.search(&query, &(top.order_by_fast_field::<DateTime>(&key, order), Count))?;
                (hits.into_iter().map(|(_, a)| (0.0, a)).collect(), total)
            }
            "size" => {
                let (hits, total) = searcher.search(&query, &(top.order_by_fast_field::<u64>("size", order), Count))?;
                (hits.into_iter().map(|(_, a)| (0.0, a)).collect(), total)
            }
            other => bail!("unknown sort {other}"),
        };
        let mut hits = Vec::with_capacity(addresses.len());
        for (score, addr) in addresses {
            let doc: TantivyDocument = searcher.doc(addr)?;
            let file_id = first_str(&doc, f.file_id).unwrap_or_default();
            let mut hit = json!({
                "file_id": file_id,
                "score": score,
                "name": first_str(&doc, f.name),
            });
            if req.with_pages && user.is_some() {
                if let Some(bp) = self.best_page_for(&file_id, req.q.as_deref().unwrap_or(""), req.tenant.as_deref())? {
                    hit["page"] = json!(bp.page);
                    hit["view"] = json!(bp.view);
                    hit["snippet"] = json!(bp.snippet);
                }
            }
            hits.push(hit);
        }
        Ok(json!({"total": total, "hits": hits}))
    }

    fn facets(&self, req: &Request) -> Result<Value> {
        let searcher = self.reader.searcher();
        let path = req.path.clone().unwrap_or_else(|| "/".to_string());
        let mut clauses = self.filters(req, "file");
        if let Some(uq) = self.user_query(req.q.as_deref().unwrap_or(""), false) {
            clauses.push((Occur::Must, uq));
        }
        let query = BooleanQuery::new(clauses);
        let mut collector = FacetCollector::for_field("facets");
        collector.add_facet(path.as_str());
        let counts = searcher.search(&query, &collector)?;
        let limit = req.limit.unwrap_or(500).clamp(1, 10_000);
        let mut children: Vec<Value> = counts
            .top_k(path.as_str(), limit)
            .into_iter()
            .map(|(facet, count)| json!({"path": facet.to_path_string(), "count": count}))
            .collect();
        children.sort_by(|a, b| a["path"].as_str().cmp(&b["path"].as_str()));
        Ok(json!({"path": path, "children": children}))
    }

    fn best_page(&self, req: &Request) -> Result<Value> {
        let file_id = req.file_id.clone().ok_or_else(|| anyhow!("best_page needs file_id"))?;
        match self.best_page_for(&file_id, req.q.as_deref().unwrap_or(""), req.tenant.as_deref())? {
            Some(bp) => Ok(json!({"found": true, "page": bp.page, "view": bp.view, "snippet": bp.snippet})),
            None => Ok(json!({"found": false})),
        }
    }

    fn best_page_for(&self, file_id: &str, q: &str, tenant: Option<&str>) -> Result<Option<BestPage>> {
        let f = self.f;
        let Some(user) = self.user_query(q, true) else { return Ok(None) };
        let searcher = self.reader.searcher();
        let mut clauses: Vec<(Occur, Box<dyn Query>)> = vec![
            (Occur::Must, Box::new(TermQuery::new(Term::from_field_text(f.kind, "page"), IndexRecordOption::Basic))),
            (Occur::Must, Box::new(TermQuery::new(Term::from_field_text(f.file_id, file_id), IndexRecordOption::Basic))),
        ];
        if let Some(t) = tenant {
            clauses.push((Occur::Must, Box::new(TermQuery::new(Term::from_field_text(f.tenant, t), IndexRecordOption::Basic))));
        }
        clauses.push((Occur::Must, user.box_clone()));
        let query = BooleanQuery::new(clauses);
        let hits = searcher.search(&query, &TopDocs::with_limit(1).order_by_score())?;
        let Some((_, addr)) = hits.into_iter().next() else { return Ok(None) };
        let doc: TantivyDocument = searcher.doc(addr)?;
        let text = first_str(&doc, f.text_stored).unwrap_or_default();
        let mut generator = SnippetGenerator::create(&searcher, &*user, f.body)?;
        generator.set_max_num_chars(160);
        let snippet = generator.snippet(&text);
        Ok(Some(BestPage {
            page: first_u64(&doc, f.page).unwrap_or(0),
            view: first_str(&doc, f.view).unwrap_or_default(),
            snippet: snippet.to_html(),
        }))
    }
}

struct BestPage {
    page: u64,
    view: String,
    snippet: String,
}

fn first_str(doc: &TantivyDocument, field: Field) -> Option<String> {
    doc.get_first(field).and_then(|v| v.as_str().map(|s| s.to_string()))
}

fn first_u64(doc: &TantivyDocument, field: Field) -> Option<u64> {
    doc.get_first(field).and_then(|v| v.as_u64())
}

/// A sort key that groups case and width variants together. Full NFKC
/// normalization is left to the Go side, which sends `name_sort` ready-made
/// when it wants a different rule; this fallback only lower-cases.
fn sort_key(name: &str) -> String {
    name.to_lowercase()
}

#[derive(Deserialize, Default)]
struct Doc {
    id: String,
    kind: String,
    #[serde(default)]
    tenant: String,
    file_id: String,
    name: Option<String>,
    ext: Option<String>,
    size: Option<u64>,
    created: Option<i64>,
    modified: Option<i64>,
    uploaded: Option<i64>,
    author: Option<String>,
    #[serde(default)]
    facets: Vec<String>,
    lat: Option<f64>,
    lon: Option<f64>,
    body: Option<String>,
    page: Option<u64>,
    view: Option<String>,
}

#[derive(Deserialize, Default)]
struct Request {
    id: Value,
    op: String,
    #[serde(default)]
    docs: Vec<Doc>,
    #[serde(default)]
    file_ids: Vec<String>,
    q: Option<String>,
    tenant: Option<String>,
    file_id: Option<String>,
    /// Facet paths the file must carry (all of them).
    #[serde(default)]
    facets_any: Vec<String>,
    /// true: direct membership (the folder itself); false: the path or anything below it.
    #[serde(default)]
    facet_exact: bool,
    path: Option<String>,
    sort: Option<String>,
    limit: Option<usize>,
    offset: Option<usize>,
    #[serde(default = "default_true")]
    with_pages: bool,
}

fn default_true() -> bool {
    true
}

#[derive(Serialize)]
struct Response {
    id: Value,
    ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
    #[serde(flatten)]
    body: BTreeMap<String, Value>,
}

fn respond(out: &mut impl Write, id: Value, result: Result<Value>) -> io::Result<()> {
    let resp = match result {
        Ok(Value::Object(map)) => Response { id, ok: true, error: None, body: map.into_iter().collect() },
        Ok(other) => Response { id, ok: true, error: None, body: BTreeMap::from([("result".to_string(), other)]) },
        Err(e) => Response { id, ok: false, error: Some(format!("{e:#}")), body: BTreeMap::new() },
    };
    serde_json::to_writer(&mut *out, &resp)?;
    out.write_all(b"\n")?;
    out.flush()
}

fn main() -> Result<()> {
    let mut dir: Option<PathBuf> = None;
    let mut tokenizer = Tokenizer::Lindera;
    let mut args = std::env::args().skip(1);
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--index-dir" => dir = Some(PathBuf::from(args.next().ok_or_else(|| anyhow!("--index-dir needs a path"))?)),
            "--tokenizer" => {
                tokenizer = match args.next().as_deref() {
                    Some("lindera") | Some("ipadic") => Tokenizer::Lindera,
                    Some("ngram") => Tokenizer::Ngram,
                    other => bail!("unknown tokenizer {other:?}"),
                }
            }
            "--version" => {
                println!("drivesearch {} schema {}", env!("CARGO_PKG_VERSION"), SCHEMA_VERSION);
                return Ok(());
            }
            other => bail!("unknown argument {other}"),
        }
    }
    let dir = dir.ok_or_else(|| anyhow!("usage: drivesearch --index-dir PATH [--tokenizer lindera|ngram]"))?;
    let stdout = io::stdout();
    let mut out = io::BufWriter::new(stdout.lock());
    let mut engine = match Engine::open(&dir, tokenizer) {
        Ok(e) => e,
        Err(e) => {
            respond(&mut out, Value::Null, Err(e))?;
            std::process::exit(2);
        }
    };
    respond(&mut out, Value::Null, Ok(json!({"event": "ready", "schema_version": SCHEMA_VERSION})))?;
    let stdin = io::stdin();
    for line in stdin.lock().lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        let req: Request = match serde_json::from_str(&line) {
            Ok(r) => r,
            Err(e) => {
                respond(&mut out, Value::Null, Err(anyhow!("bad request json: {e}")))?;
                continue;
            }
        };
        let id = req.id.clone();
        let result = engine.handle(&req);
        respond(&mut out, id, result)?;
    }
    Ok(())
}
