# OpenBrain Pro — Document Intelligence Service

> Semantic search across every document a firm has ever created, 100% local, zero lock-in.
> A Winding River Software managed service.

---

## Business Model

**Subscription-hosted managed service.** We host everything on Winding River infrastructure (mail n stuff server, Tailscale-connected). Clients never self-host.

**Full data portability — the trust signal:**
- Clients receive a backup of their original documents (untouched) at any time
- Clients receive a full export of the RAG database (embeddings, extracted thoughts, metadata)
- They can leave at any time, take everything, and run it themselves
- No lock-in. The portability is what makes professionals comfortable signing up

**Why they stay:**
- We handle hosting, updates, model upgrades, maintenance
- Search quality improves over time as we upgrade models and extraction
- New features (graph visualization, cross-matter search, timeline views) just appear
- It just works — they search, they find things
- They don't want to run Ollama/PostgreSQL/pgvector themselves

---

## Target Market

### Primary: Small Law Firms (2-20 attorneys)
- Thousands of documents per matter (contracts, filings, correspondence, case law)
- Attorney-client privilege requires data control (ABA Model Rule 1.6)
- Current options: expensive cloud DMS ($40-80/user/month) or Windows Explorer search
- Pain: "Where's that contract where we agreed to the indemnification clause?"

### Secondary: CPA / Accounting Firms (2-15 professionals)
- Tax returns, engagement letters, financials, correspondence
- IRS Pub 4557 + SOC 2 concerns around data handling
- Pain: "Pull up everything we have on the Johnson estate from last year"

### Why These Clients
- High document volume, high value per document
- Regulatory pressure to control data location
- Willing to pay for tools that save billable hours
- Accustomed to subscription services (Westlaw, LexisNexis, QuickBooks)
- Small enough that enterprise DMS is overkill/overpriced

---

## Competitive Advantages

| Feature | NetDocuments / iManage | OpenBrain Pro |
|---------|------------------------|---------------|
| Data location | Vendor's cloud | Client's tailnet (our server) |
| Semantic search | Basic keyword only | Vector + hybrid (meaning-based) |
| AI extraction | Cloud LLM (data leaves network) | Local Ollama (nothing leaves) |
| Data portability | Vendor lock-in, export fees | Full backup anytime, zero fees |
| Pricing | $40-80/user/month | TBD — competitive flat rate |
| Setup | Weeks + consultant | Join tailnet, start uploading |
| Network exposure | Public internet | Tailscale only (zero attack surface) |
| OCR / scanned docs | Usually add-on | Built-in (PyMuPDF) |

---

## Architecture

```
Mail n Stuff (Winding River server, Tailscale)
┌──────────────────────────────────────────────┐
│                                              │
│  ┌─ Firm A (isolated) ─┐  ┌─ Firm B ──────┐ │
│  │ PostgreSQL + pgvector│  │ PostgreSQL    │ │
│  │ Document store       │  │ Document store│ │
│  │ Embeddings + thoughts│  │ Embeddings   │ │
│  └──────────────────────┘  └──────────────┘ │
│                                              │
│  ┌─ Shared Services ───────────────────────┐ │
│  │ Ollama (local LLM — extraction/OCR)     │ │
│  │ Caddy (reverse proxy, Tailscale auth)   │ │
│  │ OpenBrain Web App (per-tenant routing)  │ │
│  │ Ingestion Worker (PDF/DOCX → text → DB) │ │
│  └─────────────────────────────────────────┘ │
│                                              │
│              Tailscale only                  │
│         (no public internet exposure)        │
└──────────────────────────────────────────────┘
         │                         │
    Firm users                Winding River
    (any device on             (remote mgmt
     their tailnet)             via tailnet)
```

### Per-Firm Isolation
- Separate PostgreSQL database per firm (not just schema — full DB isolation)
- Document storage isolated per firm
- Backups are per-firm (one `pg_dump` + document tar = complete portable export)
- Shared Ollama instance is stateless (no client data retained in model)

### Tailscale as the Network Layer
- Zero firewall configuration for clients
- Tailscale identity = user identity (no password management)
- ACLs control which firm users see which OpenBrain instance
- Winding River gets management access via shared tailnet or invited node

---

## Document Ingestion Pipeline

```
Document (PDF/DOCX/PPTX/etc)
    │
    ▼
ingest.py — text extraction
    │  PyMuPDF (PDF + OCR)
    │  python-docx (Word)
    │  markitdown (everything else)
    │
    ▼
Chunking (long docs split into ~2000 char windows)
    │
    ▼
extract.py — LLM structuring (existing, via Ollama)
    │  Extracts: facts, decisions, people, dates, clauses
    │  Tags automatically by matter/folder
    │
    ▼
embeddings.py — vector embedding (existing, bge-small-en-v1.5)
    │
    ▼
PostgreSQL + pgvector (existing)
    │  Original document stored as blob or file reference
    │  Extracted text chunks stored with embeddings
    │  Metadata: source file, page number, matter, firm
    │
    ▼
Searchable via web UI, MCP, CLI
```

### Supported Formats (Priority Order)
1. **PDF** — PyMuPDF with built-in OCR for scanned documents
2. **DOCX** — python-docx for paragraphs, tables, headers
3. **PPTX/XLSX** — markitdown (Microsoft's library)
4. **Plain text / Markdown** — direct ingestion
5. **Images** — OCR via PyMuPDF or tesseract
6. **Email (.eml/.msg)** — future: extract body + attachments

### Chunking Strategy
- Split by page (PDFs) or section (DOCX headers)
- ~2000 character windows with 200 char overlap
- Each chunk retains metadata: source file, page/section, matter
- Chunks are independently searchable but link back to source document

---

## Data Export / Portability Format

When a client leaves, they receive:

```
firm-export-2026-03-21/
├── documents/              # Original files, untouched
│   ├── matter-001/
│   │   ├── contract.pdf
│   │   └── correspondence.docx
│   └── matter-002/
│       └── filing.pdf
├── database/
│   ├── firm_dump.sql       # Full pg_dump — thoughts, embeddings, metadata
│   └── schema.sql          # Schema to recreate from scratch
├── README.md               # How to restore this on your own PostgreSQL
└── manifest.json           # File inventory with checksums
```

They can:
- Load `firm_dump.sql` into any PostgreSQL 17 + pgvector instance
- Point a stock OpenBrain at it and have full search immediately
- Or just keep the documents and ignore the database

---

## Build Phases

### Phase 0: Ingestion Layer (NOW — personal use)
- [ ] `ingest.py` module — PDF/DOCX/PPTX text extraction
- [ ] `ingest_document` MCP tool
- [ ] Folder watcher for idea repository (`~/ideas/`)
- [ ] Auto-tag by folder name
- [ ] Chunking for long documents
- [ ] Store source file reference in thought metadata

### Phase 1: Web UI for Non-Technical Users
- [ ] Upload documents via web interface (drag and drop)
- [ ] Search results show source document + page number
- [ ] "View original" link to download/view the source PDF
- [ ] Ingestion status/progress indicator
- [ ] Matter/folder organization in UI

### Phase 2: Multi-Tenant Isolation
- [ ] Per-firm database creation and routing
- [ ] Tailscale identity-based auth (who are you on the tailnet)
- [ ] Firm admin can manage users and matters
- [ ] Per-firm backup/export tooling
- [ ] Tenant provisioning CLI (`openbrain-admin create-firm ...`)

### Phase 3: Professional Features
- [ ] OCR pipeline for scanned documents
- [ ] Email ingestion (.eml attachments)
- [ ] Cross-matter search with access controls
- [ ] Document timeline (version tracking across related documents)
- [ ] Audit log (who searched what, when — compliance requirement)
- [ ] Scheduled re-ingestion (pick up new files from watched folders)

### Phase 4: Polish & Go-to-Market
- [ ] Export/portability tooling (one-command full backup)
- [ ] Onboarding flow (firm joins tailnet → first upload → first search)
- [ ] Usage dashboard (document count, search volume, storage)
- [ ] Pricing model finalized
- [ ] Marketing site for Winding River Software

---

## Technical Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Embedding model | bge-small-en-v1.5 (CPU) | Must run without GPU on server hardware |
| LLM extraction | Ollama + Gemma3/Llama3.2 | 100% local, no API keys, no data exfil |
| PDF extraction | PyMuPDF | Fast, built-in OCR, handles scanned docs |
| DOCX extraction | python-docx | Reliable, pure Python |
| Catch-all formats | markitdown | Microsoft's lib handles PPTX, XLSX, HTML |
| Auth | Tailscale identity | Zero password management, network-level trust |
| Tenant isolation | Separate PostgreSQL DB per firm | Clean backup/export, no cross-contamination |
| Deployment | Docker Compose on mail n stuff | Single server, Tailscale-only access |
| Export format | pg_dump + original files + manifest | Fully portable, client can self-host |

---

## Open Questions

- [ ] Pricing model: per-firm flat rate? Per-user? Per-GB? Tiered?
- [ ] Mail n stuff server specs — RAM/CPU/storage capacity, GPU availability?
- [ ] Maximum document volume per firm (affects storage and embedding time)?
- [ ] Do we need to support concurrent multi-firm Ollama inference, or is sequential OK?
- [ ] Legal: any liability concerns hosting attorney-client privileged material?
- [ ] Marketing: approach law firms directly, or partner with legal tech consultants?
