#!/usr/bin/env python3
"""
build-brain-viz.py — OpenBrain thought visualizer builder.

Reads all thoughts + embeddings from Postgres, projects to 2D via UMAP,
clusters with HDBSCAN, generates LLM cluster labels via Ollama, and emits
brain.json into cmd/openbrain-web/static/ so the Go web server can serve it.

Clustering runs in a moderate-dimensional UMAP space (see --cluster-dim),
NOT the raw 768d embeddings and NOT the 2D layout projection. HDBSCAN's
density estimate collapses in 768d (distance concentration), and the 2D
layout throws away almost all topical structure; a 10-30d UMAP space keeps
the structure while giving HDBSCAN a space where density is meaningful. The
2D projection is computed separately and used only for x/y node layout.

Usage:
    python3 scripts/build-brain-viz.py

Options:
    --output PATH       Write JSON to PATH (default: cmd/openbrain-web/static/brain.json)
    --standalone FILE   Also write a self-contained HTML file (for offline use)
    --kmeans K          Use k-means with K clusters instead of HDBSCAN
    --no-llm            Skip LLM labeling; use heuristic labels only
    --min-cluster N     HDBSCAN min_cluster_size (default: 5)
    --min-samples N     HDBSCAN min_samples; lower = fewer noise points
                        (default: 1)
    --cluster-dim N     UMAP dimensionality of the clustering space
                        (default: 15; use 0 to cluster raw embeddings)
    --max-edges N       Max similarity edges (default: 2000)
    --sim-thresh F      Min cosine similarity for an edge (default: 0.55)
    --progress-file PATH  Write a progress status JSON to PATH as generation
                        proceeds (default: no progress file written; the
                        script behaves exactly as before this flag existed)

Requirements (install in a venv):
    pip install -r scripts/requirements-viz.txt
"""

import argparse
import json
import os
import re
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

import numpy as np
import requests
from dotenv import dotenv_values


def _import_umap():
    """Return the UMAP class, or None if umap-learn is not installed.

    Callers print their own fallback message: project_2d() falls back to
    t-SNE, but cluster_space() falls back to raw L2-normalized embeddings,
    so no single message here is accurate for both call sites.
    """
    try:
        from umap import UMAP
        return UMAP
    except ImportError:
        return None


def _import_hdbscan():
    try:
        import hdbscan as hdbscan_mod
        return hdbscan_mod.HDBSCAN
    except ImportError:
        print("  [warn] hdbscan not installed; will use k-means")
        return None


def load_thoughts(cfg: dict) -> list[dict]:
    import psycopg
    from pgvector.psycopg import register_vector

    host = cfg.get("OPENBRAIN_DB_HOST", "localhost")
    port = cfg.get("OPENBRAIN_DB_PORT", "5432")
    dbname = cfg.get("OPENBRAIN_DB_NAME", "openbrain")
    user = cfg.get("OPENBRAIN_DB_USER", "openbrain")
    password = cfg.get("OPENBRAIN_DB_PASSWORD")
    if not password:
        print("ERROR: OPENBRAIN_DB_PASSWORD is not set (check .env)", file=sys.stderr)
        sys.exit(1)

    if host.startswith("/"):
        conn_str = f"host={host} port={port} dbname={dbname} user={user} password={password}"
    else:
        conn_str = f"host={host} port={port} dbname={dbname} user={user} password={password} sslmode=disable"

    print(f"  Connecting to Postgres ({dbname} @ {host})…")
    with psycopg.connect(conn_str) as conn:
        register_vector(conn)
        rows = conn.execute(
            """
            SELECT id::text, content, summary, thought_type,
                   tags, created_at::text, embedding
            FROM thoughts
            WHERE is_current = TRUE AND embedding IS NOT NULL
            ORDER BY created_at
            """,
        ).fetchall()

    thoughts = []
    for row in rows:
        id_, content, summary, ttype, tags, created_at, embedding = row
        thoughts.append({
            "id": id_,
            "content": content or "",
            "summary": summary or "",
            "thought_type": ttype or "note",
            "tags": list(tags) if tags else [],
            "created_at": created_at or "",
            "embedding": embedding.to_numpy().astype(np.float32),
        })

    print(f"  Loaded {len(thoughts)} thoughts with embeddings")
    return thoughts


def project_2d(embeddings: np.ndarray, use_umap: bool = True) -> np.ndarray:
    N = len(embeddings)
    if use_umap:
        UMAP = _import_umap()
        if UMAP is not None:
            # n_neighbors must be < N; clamp so small brains don't crash.
            n_neighbors = min(15, N - 1)
            print(f"  Running UMAP (cosine, n_neighbors={n_neighbors}, min_dist=0.1)…")
            reducer = UMAP(n_components=2, metric="cosine",
                           n_neighbors=n_neighbors, min_dist=0.1,
                           random_state=42, verbose=False)
            return reducer.fit_transform(embeddings)
        print("  [warn] umap-learn not installed; falling back to t-SNE")

    from sklearn.manifold import TSNE
    # perplexity must be < N; clamp so small brains don't crash.
    perplexity = min(30, max(5, (N - 1) // 3))
    print(f"  Running t-SNE (cosine, perplexity={perplexity})…")
    tsne = TSNE(n_components=2, metric="cosine", perplexity=perplexity,
                max_iter=1000, random_state=42)
    return tsne.fit_transform(embeddings)


def _l2_normalize(embeddings: np.ndarray) -> np.ndarray:
    """Return row-wise L2-normalized copy so euclidean distance tracks cosine."""
    norms = np.linalg.norm(embeddings, axis=1, keepdims=True)
    norms = np.where(norms == 0, 1, norms)
    return embeddings / norms


def cluster_space(embeddings: np.ndarray, n_components: int = 15) -> np.ndarray:
    """Reduce embeddings to the space HDBSCAN clusters in.

    Clustering the raw 768d embeddings collapses HDBSCAN to a couple of
    dense pockets because pairwise distances concentrate in high dimensions
    (curse of dimensionality); clustering the 2D layout projection discards
    almost all topical structure. A moderate UMAP space (default 15d) keeps
    the topical structure while giving HDBSCAN meaningful density contrast.

    n_components == 0, or UMAP being unavailable, falls back to the raw
    L2-normalized embeddings so the pipeline still runs (degraded).
    """
    N = len(embeddings)
    if n_components <= 0:
        print("  Clustering space: raw L2-normalized embeddings (--cluster-dim 0)")
        return _l2_normalize(embeddings)

    UMAP = _import_umap()
    if UMAP is None:
        print("  [warn] umap-learn unavailable; clustering raw L2-normalized embeddings")
        return _l2_normalize(embeddings)

    # n_neighbors and n_components must be < N; clamp for small brains.
    n_neighbors = min(15, N - 1)
    n_components = min(n_components, N - 1)
    print(f"  Reducing to {n_components}d clustering space "
          f"(UMAP cosine, n_neighbors={n_neighbors}, min_dist=0.0)…")
    # min_dist=0.0 packs points as tightly as the manifold allows, which is
    # the recommended setting when the projection feeds a density clusterer.
    reducer = UMAP(n_components=n_components, metric="cosine",
                   n_neighbors=n_neighbors, min_dist=0.0,
                   random_state=42, verbose=False)
    return reducer.fit_transform(embeddings)


def cluster_hdbscan(
    space: np.ndarray,
    min_cluster_size: int = 5,
    min_samples: int = 1,
) -> np.ndarray | None:
    """Cluster the (already reduced) *space* with HDBSCAN.

    *space* is expected to be the moderate-dimensional clustering space from
    cluster_space(), not the raw embeddings; euclidean distance is applied
    directly with no further normalization.
    """
    HDBSCAN = _import_hdbscan()
    if HDBSCAN is None:
        return None
    print(f"  Running HDBSCAN (min_cluster_size={min_cluster_size}, "
          f"min_samples={min_samples})…")
    clusterer = HDBSCAN(min_cluster_size=min_cluster_size, min_samples=min_samples,
                        metric="euclidean", cluster_selection_epsilon=0.0)
    labels = clusterer.fit_predict(space)
    n_clusters = len(set(labels)) - (1 if -1 in labels else 0)
    print(f"  HDBSCAN: {n_clusters} clusters, {(labels == -1).sum()} noise points")
    return labels


def cluster_kmeans(embeddings: np.ndarray, k: int = 8) -> np.ndarray:
    from sklearn.cluster import KMeans
    print(f"  Running k-means (k={k})…")
    labels = KMeans(n_clusters=k, random_state=42, n_init=10).fit_predict(embeddings)
    print(f"  k-means: {k} clusters")
    return labels


def compute_edges(
    embeddings: np.ndarray,
    sim_thresh: float = 0.55,
    max_edges: int = 2000,
    top_k: int = 5,
) -> list[dict]:
    print(f"  Computing cosine similarity edges (thresh={sim_thresh}, top_k={top_k})…")
    norms = np.linalg.norm(embeddings, axis=1, keepdims=True)
    norms = np.where(norms == 0, 1, norms)
    normed = (embeddings / norms).astype(np.float32)

    edges = []
    seen: set[tuple[int, int]] = set()
    N = len(normed)
    chunk = 200
    for start in range(0, N, chunk):
        batch = normed[start:start + chunk]
        sims = batch @ normed.T
        for bi, row in enumerate(sims):
            i = start + bi
            row[i] = -1
            for j in np.argsort(row)[::-1][:top_k]:
                w = float(row[j])
                if w < sim_thresh:
                    break
                key = (min(i, int(j)), max(i, int(j)))
                if key not in seen:
                    seen.add(key)
                    edges.append({"s": key[0], "t": key[1], "w": round(w, 3)})

    edges.sort(key=lambda e: -e["w"])
    edges = edges[:max_edges]
    print(f"  Edges kept: {len(edges)}")
    return edges


def heuristic_label(cluster_thoughts: list[dict]) -> str:
    from collections import Counter
    tag_counts: Counter = Counter()
    type_counts: Counter = Counter()
    for t in cluster_thoughts:
        for tag in t["tags"]:
            tag_counts[tag.lower()] += 1
        type_counts[t["thought_type"]] += 1
    top_tags = [tag for tag, _ in tag_counts.most_common(2)]
    if top_tags:
        return ", ".join(top_tags).title()
    dominant = type_counts.most_common(1)[0][0] if type_counts else "note"
    return dominant.replace("_", " ").title()


# Strips a leading bullet run (-, *, •) or a numbered-list marker
# (N. / N)) followed by required whitespace. Bare leading digits with no
# "." or ")" delimiter (a real label like "3-Phase Feeder Data" or "2026
# Roadmap") are deliberately NOT matched, so they survive untouched.
_LIST_MARKER_RE = re.compile(r"^\s*(?:[\-\*•]+|\d+[.\)])\s+")


def _parse_llm_label(raw: str) -> str:
    """Extract a usable label from a (possibly chatty) LLM response.

    Ollama models vary in how strictly they follow "return only the label":
    some return a bulleted or numbered list of candidates, a preamble
    sentence before the list, or bold-wrapped markdown. For each non-empty
    line: strip a leading list marker, then strip surrounding whitespace,
    quotes, backticks, and asterisks. A line that still ends with a colon
    after stripping is treated as a preamble ("Here are some labels:") and
    skipped in favor of the next non-empty line. Returns an empty string
    when no usable line is found, so the caller's length check rejects it
    the same as an empty response would.
    """
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        line = _LIST_MARKER_RE.sub("", line)
        line = line.strip(" \t\"'`*")
        if not line or line.endswith(":"):
            continue
        return line
    return ""


def llm_label(cluster_thoughts: list[dict], cfg: dict, model: str) -> str | None:
    sample = sorted(cluster_thoughts,
                    key=lambda t: (len(t["summary"]) > 0, len(t["content"])),
                    reverse=True)[:8]
    snippets = [f"- {t['summary'].strip() or t['content'][:200].strip()}" for t in sample]
    prompt = (
        "You are helping label regions of a personal knowledge brain map. "
        "Below are related notes from one cluster. "
        "Give a SHORT 2-4 word label naming the theme of this region, "
        "like a brain lobe name. Examples: 'Work Decisions', 'People & Meetings', "
        "'Technical Ideas', 'Life Goals'. Return ONLY the label, nothing else.\n\n"
        + "\n".join(snippets)
    )
    base_url = cfg.get("OPENBRAIN_OLLAMA_BASE_URL", "http://localhost:11434")
    try:
        resp = requests.post(
            f"{base_url}/api/generate",
            json={
                "model": model,
                "prompt": prompt,
                "stream": False,
                # Region labels are short classification outputs; hidden
                # chain-of-thought adds latency without improving the result.
                "think": False,
                # Fix temperature and seed so labels are stable across rebuilds
                # when the underlying data hasn't changed.
                "options": {"temperature": 0, "seed": 42},
            },
            timeout=30,
        )
        resp.raise_for_status()
        label = _parse_llm_label(resp.json().get("response", ""))
        if 2 <= len(label) <= 60:
            return label
    except Exception as exc:
        print(f"    [warn] LLM call failed: {exc}")
    return None


def build_data(
    thoughts: list[dict],
    coords_2d: np.ndarray,
    labels: list[int],
    edges: list[dict],
    clusters_out: list[dict],
    cfg: dict,
) -> dict:
    xs, ys = coords_2d[:, 0], coords_2d[:, 1]
    margin = 0.05
    xr, yr = xs.max() - xs.min(), ys.max() - ys.min()
    thought_types = sorted(set(t["thought_type"] for t in thoughts))

    nodes_out = []
    for i, t in enumerate(thoughts):
        preview = t["summary"].strip() or t["content"][:300].strip()
        nodes_out.append({
            "id": t["id"],
            "x": round(float(coords_2d[i, 0]), 4),
            "y": round(float(coords_2d[i, 1]), 4),
            "cluster": labels[i],
            "type": t["thought_type"],
            "tags": t["tags"][:5],
            "summary": t["summary"][:200],
            "content": preview[:300],
            "created_at": t["created_at"],
        })

    return {
        "nodes": nodes_out,
        "edges": edges,
        "clusters": clusters_out,
        "meta": {
            "thought_types": thought_types,
            "bounds": [
                round(float(xs.min() - xr * margin), 4),
                round(float(xs.max() + xr * margin), 4),
                round(float(ys.min() - yr * margin), 4),
                round(float(ys.max() + yr * margin), 4),
            ],
            "n_thoughts": len(thoughts),
            "n_clusters": len([c for c in clusters_out if c["id"] >= 0]),
            "n_edges": len(edges),
            "embedding_model": cfg.get("OPENBRAIN_EMBEDDING_MODEL", "unknown"),
        },
    }


def _atomic_write(path: Path, text: str) -> None:
    """Write *text* to *path* atomically via a temp-file rename.

    The temp file is created in the same directory so os.replace() is a
    same-filesystem rename (no cross-device copy).  If the write or the
    subsequent JSON sanity-check fails the original file is left untouched.
    """
    fd, tmp = tempfile.mkstemp(dir=path.parent, suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(text)
        # Quick sanity-check: re-parse to catch truncation/encoding errors.
        json.loads(Path(tmp).read_text(encoding="utf-8"))
        os.replace(tmp, path)
    except Exception:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


def _write_progress(
    progress_path: Path | None,
    phase: str,
    pct: int,
    clusters_done: int = 0,
    clusters_total: int = 0,
) -> None:
    """Best-effort atomic write of the progress sidecar JSON.

    No-op when *progress_path* is None, which is the default and keeps
    plain `make viz` / no-flag runs byte-for-byte unchanged. The write is
    atomic (temp file in the same directory, then os.replace) so a reader
    polling this path never observes a partial document. Unlike
    `_atomic_write`, failures here are logged and swallowed rather than
    raised: progress reporting is telemetry for the async job status
    endpoint, not part of the pipeline's actual output, so a sidecar write
    failure must never abort real generation work.
    """
    if progress_path is None:
        return
    payload: dict[str, str | int | float] = {
        "pct": pct,
        "phase": phase,
        "clusters_done": clusters_done,
        "clusters_total": clusters_total,
        "ts": time.time(),
    }
    text = json.dumps(payload)
    try:
        progress_path.parent.mkdir(parents=True, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=progress_path.parent, suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                f.write(text)
            os.replace(tmp, progress_path)
        except Exception:
            try:
                os.unlink(tmp)
            except OSError:
                pass
            raise
    except Exception as exc:
        print(f"  [warn] failed to write progress file {progress_path}: {exc}", file=sys.stderr)


def parse_args(default_output: Path) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build OpenBrain brain visualizer data")
    parser.add_argument("--output", default=str(default_output), metavar="PATH",
                        help=f"Write JSON to PATH (default: {default_output})")
    parser.add_argument("--standalone", metavar="FILE",
                        help="Write a self-contained HTML file to FILE (offline use)")
    parser.add_argument("--kmeans", type=int, default=0, metavar="K",
                        help="Use k-means with K clusters instead of HDBSCAN")
    parser.add_argument("--no-llm", action="store_true",
                        help="Skip LLM labeling; use heuristic labels only")
    parser.add_argument("--min-cluster", type=int, default=5, metavar="N",
                        help="HDBSCAN min_cluster_size (default: 5)")
    parser.add_argument("--min-samples", type=int, default=1, metavar="N",
                        help="HDBSCAN min_samples; lower = fewer noise points (default: 1)")
    parser.add_argument("--cluster-dim", type=int, default=15, metavar="N",
                        help="UMAP dimensionality of the clustering space "
                             "(default: 15; use 0 to cluster raw embeddings)")
    parser.add_argument("--max-edges", type=int, default=2000, metavar="N",
                        help="Max similarity edges (default: 2000)")
    parser.add_argument("--sim-thresh", type=float, default=0.55, metavar="F",
                        help="Min cosine similarity for an edge (default: 0.55)")
    parser.add_argument("--progress-file", default=None, metavar="PATH",
                        help="Write a progress status JSON to PATH as generation "
                             "proceeds (default: no progress file written)")
    return parser.parse_args()


def load_config(script_dir: Path, repo_root: Path) -> dict:
    """Load config from .env (if present) merged with real environment variables.

    .env values take precedence so a local override file works as expected, but
    environment variables set without a .env file are also honoured (e.g. in CI
    or Docker).
    """
    env_path = None
    for candidate in [script_dir, repo_root]:
        p = candidate / ".env"
        if p.exists():
            env_path = p
            break

    # Start from the real environment so Docker / CI setups work without a .env.
    cfg: dict[str, Any] = dict(os.environ)
    if env_path:
        print(f"  Loading .env from {env_path}")
        # .env values override env vars (local dev takes precedence).
        cfg.update(dotenv_values(env_path))
    else:
        print("  [warn] .env not found; using environment variables / defaults")
    return cfg


def resolve_ollama_model(cfg: dict) -> str:
    """Return the Ollama model for cluster labeling.

    Checks OPENBRAIN_EXTRACT_MODEL, then OPENBRAIN_EXTRACT_MODEL_FAST, then
    OPENBRAIN_CHAT_MODEL (in that order). Exits with an error if none is set;
    pass --no-llm to skip cluster labeling without needing any model var.

    OPENBRAIN_EXTRACT_MODEL is checked first, ahead of the FAST variant,
    because labeling quality was measured directly on this brain's clusters:
    the fast model (llama3.2:1b) reliably ignores the "return only the
    label" instruction and returns a rambling multi-line list of candidates
    or a meta preamble ("Here are the labels..."), producing a usable but
    semantically empty label even after robust first-line parsing.
    OPENBRAIN_EXTRACT_MODEL (gemma3 on this brain) consistently returns a
    single descriptive phrase. Labeling is a bounded, one-call-per-cluster
    cost (dozens of calls, not per-thought), so the extra latency of the
    larger model is worth the label quality.
    """
    model = (
        cfg.get("OPENBRAIN_EXTRACT_MODEL") or
        cfg.get("OPENBRAIN_EXTRACT_MODEL_FAST") or
        cfg.get("OPENBRAIN_CHAT_MODEL")
    )
    if not model:
        sys.exit(
            "error: set one of OPENBRAIN_EXTRACT_MODEL, "
            "OPENBRAIN_EXTRACT_MODEL_FAST, or OPENBRAIN_CHAT_MODEL in .env "
            "(or pass --no-llm to skip cluster labeling)"
        )
    return model


def label_clusters(
    thoughts: list[dict],
    coords_2d: np.ndarray,
    labels: list[int],
    cfg: dict,
    ollama_model: str,
    no_llm: bool,
    progress_path: Path | None = None,
) -> tuple[list[dict], int, int]:
    """Build cluster metadata with labels.

    Returns (clusters_out, llm_attempts, llm_fallbacks).

    This is the dominant, minute-scale cost (one sequential Ollama call per
    cluster), so it is the granular part of the progress signal: pct climbs
    from 50 to 95 as clusters_done advances toward clusters_total.
    """
    clusters_out: list[dict] = []
    llm_attempts = 0
    llm_fallbacks = 0

    cluster_ids = sorted(set(labels))
    clusters_total = len(cluster_ids)
    clusters_done = 0
    _write_progress(progress_path, "labeling", 50, clusters_done, clusters_total)

    for cid in cluster_ids:
        members = [thoughts[i] for i, lbl in enumerate(labels) if lbl == cid]
        member_coords = [coords_2d[i] for i, lbl in enumerate(labels) if lbl == cid]
        heuristic = heuristic_label(members)

        if cid < 0:
            label = "Unclustered"
        elif no_llm:
            label = heuristic
            print(f"  Cluster {cid:3d} ({len(members):3d} thoughts) → {label}  [heuristic]")
        else:
            llm_attempts += 1
            print(f"  Cluster {cid:3d} ({len(members):3d} thoughts) → asking LLM…", end="", flush=True)
            llm = llm_label(members, cfg, ollama_model)
            if llm:
                label = llm
                print(f' ✓  "{label}"')
            else:
                label = heuristic
                llm_fallbacks += 1
                print(f' fallback → "{label}"')

        arr = np.array(member_coords)
        clusters_out.append({
            "id": cid,
            "label": label,
            "heuristic": heuristic,
            "size": len(members),
            "cx": round(float(arr[:, 0].mean()), 4),
            "cy": round(float(arr[:, 1].mean()), 4),
        })

        clusters_done += 1
        # clusters_total is len(cluster_ids); this loop only runs when
        # cluster_ids is non-empty (we're iterating over it), so
        # clusters_total is always >= 1 here and the division is safe.
        pct = 50 + int(45 * clusters_done / clusters_total)
        _write_progress(progress_path, "labeling", pct, clusters_done, clusters_total)

    return clusters_out, llm_attempts, llm_fallbacks


def build_standalone_html(data_json: str, graph_html_path: Path) -> str:
    """Return a self-contained HTML file with the renderer and data inlined.

    Reads graph.html, strips its fetch('/brain.json'…) bootstrap, and injects
    the data as a module-level constant so the file works without a server.
    """
    renderer = graph_html_path.read_text(encoding="utf-8")
    # Replace the async fetch with a synchronous data injection so the
    # standalone file works without any server or network request.
    renderer = renderer.replace(
        "const res = await fetch('/brain.json' + location.search);",
        "// standalone mode: data is inlined below\n    const res = { ok: true, json: async () => __STANDALONE_DATA__ };",
    )
    # Escape characters that could break out of an inline <script> block:
    # </script> terminates the element; < and > risk misparse; & starts entity
    # sequences; U+2028/U+2029 are JS line-terminators not allowed in strings.
    # \uXXXX escapes are valid in JS string literals and decode transparently.
    safe_json = (
        data_json
        .replace("&", "\u0026")
        .replace("<", "\u003c")
        .replace(">", "\u003e")
        .replace(" ", r" ")
        .replace(" ", r" ")
    )
    # Inject the data constant before the closing </body> tag.
    injection = f'<script>const __STANDALONE_DATA__ = {safe_json};</script>'
    renderer = renderer.replace("</body>", f"{injection}\n</body>", 1)
    return renderer


def write_output(
    data_json: str,
    output_path: Path,
    standalone_path: Path | None,
    graph_html_path: Path,
) -> None:
    """Write brain.json (atomically) and optionally a standalone HTML file."""
    output_path.parent.mkdir(parents=True, exist_ok=True)
    _atomic_write(output_path, data_json)
    size_kb = output_path.stat().st_size / 1024
    print(f"\n✅  brain.json written → {output_path} ({size_kb:.0f} KB)")

    if standalone_path is not None:
        html = build_standalone_html(data_json, graph_html_path)
        standalone_path.parent.mkdir(parents=True, exist_ok=True)
        _atomic_write(standalone_path, html)
        print(f"   standalone HTML → {standalone_path}")


def run_pipeline(args: argparse.Namespace) -> None:
    script_dir = Path(__file__).parent
    repo_root = script_dir.parent
    graph_html_path = repo_root / "cmd" / "openbrain-web" / "static" / "graph.html"

    progress_path = Path(args.progress_file) if args.progress_file else None

    cfg = load_config(script_dir, repo_root)

    # Resolve the Ollama model up-front only when LLM labeling is requested.
    # This ensures --no-llm never fails due to a missing model configuration.
    ollama_model: str = ""
    if not args.no_llm:
        ollama_model = resolve_ollama_model(cfg)
        print(f"  LLM model: {ollama_model}")

    print("\n[1/6] Loading thoughts from database…")
    _write_progress(progress_path, "loading", 5)
    thoughts = load_thoughts(cfg)
    if not thoughts:
        print("ERROR: no thoughts found")
        sys.exit(1)

    embeddings_arr = np.stack([t["embedding"] for t in thoughts])

    print("\n[2/6] Projecting embeddings to 2D…")
    _write_progress(progress_path, "projecting", 15)
    coords_2d = project_2d(embeddings_arr, use_umap=(args.kmeans == 0))

    print("\n[3/6] Clustering…")
    _write_progress(progress_path, "clustering", 35)
    if args.kmeans > 0:
        labels = cluster_kmeans(embeddings_arr, k=args.kmeans)
    else:
        # Cluster in a moderate-dim UMAP space, not the raw 768d embeddings
        # (distance concentration collapses HDBSCAN) and not the 2D layout
        # projection (discards topical structure). See cluster_space().
        clustering_input = cluster_space(embeddings_arr, n_components=args.cluster_dim)
        labels = cluster_hdbscan(clustering_input, min_cluster_size=args.min_cluster,
                                 min_samples=args.min_samples)
        if labels is None:
            print("  [fallback] using k-means k=8")
            labels = cluster_kmeans(embeddings_arr, k=8)

    unique_non_noise = sorted(set(labels) - {-1})
    id_map = {old: new for new, old in enumerate(unique_non_noise)}
    id_map[-1] = -1
    labels = [id_map[lbl] for lbl in labels]

    print("\n[4/6] Computing similarity edges…")
    _write_progress(progress_path, "edges", 45)
    edges = compute_edges(embeddings_arr, sim_thresh=args.sim_thresh,
                          max_edges=args.max_edges)

    print("\n[5/6] Generating cluster labels…")
    clusters_out, llm_attempts, llm_fallbacks = label_clusters(
        thoughts, coords_2d, labels, cfg, ollama_model, args.no_llm,
        progress_path=progress_path,
    )

    # Warn loudly if the LLM service was completely unreachable, and print a
    # machine-readable marker line so callers (the Go rebuild handler) can
    # detect the degraded build from captured stdout/stderr without having to
    # parse the human-readable warning text.
    if llm_attempts > 0 and llm_fallbacks == llm_attempts:
        print(f"\n  [warn] All {llm_attempts} clusters fell back to heuristic labels, "
              f"is Ollama reachable at {cfg.get('OPENBRAIN_OLLAMA_BASE_URL', 'http://localhost:11434')}?")
        print("BRAIN_VIZ_DEGRADED=true")
    elif llm_fallbacks > 0:
        print(f"\n  [info] {llm_fallbacks}/{llm_attempts} clusters used heuristic labels")

    print("\n[6/6] Writing output…")
    total_clusters = len(clusters_out)
    _write_progress(progress_path, "writing", 95, total_clusters, total_clusters)
    data = build_data(thoughts, coords_2d, labels, edges, clusters_out, cfg)
    data_json = json.dumps(data, ensure_ascii=False)

    output_path = Path(args.output)
    standalone_path = Path(args.standalone) if args.standalone else None
    write_output(data_json, output_path, standalone_path, graph_html_path)
    _write_progress(progress_path, "done", 100, total_clusters, total_clusters)

    print(f"   {data['meta']['n_thoughts']} nodes · {data['meta']['n_clusters']} clusters · {data['meta']['n_edges']} edges")
    print(f"\n   Rebuild anytime:  make viz")
    print(f"   Then open:        http://127.0.0.1:10203/graph")


def main() -> None:
    script_dir = Path(__file__).parent
    repo_root = script_dir.parent
    default_output = repo_root / "cmd" / "openbrain-web" / "static" / "brain.json"
    args = parse_args(default_output)
    run_pipeline(args)


if __name__ == "__main__":
    main()
