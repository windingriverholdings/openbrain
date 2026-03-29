// Package docparse metadata constants for standardized thought metadata keys.
package docparse

// Standard metadata keys populated during document ingestion.
const (
	MetaSourceFile   = "source_file"   // basename of original file
	MetaSourcePath   = "source_path"   // path relative to ingest dir (never absolute)
	MetaSourceFormat = "source_format" // "pdf", "docx", "text", etc.
	MetaChunkIndex   = "chunk_index"   // 0-based chunk index
	MetaChunkTotal   = "chunk_total"   // total number of chunks
	MetaPageNumber   = "page_number"   // page number when available
	MetaIngestedAt   = "ingested_at"   // ISO 8601 timestamp (RFC3339)
)
