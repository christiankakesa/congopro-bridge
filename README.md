# Congopro Bridge 🔍

A hybrid full-text + semantic search engine for the company directory, built in Go.

## Architecture

```
companies.json
     │
     ├──► Bleve (in-memory BM25 full-text index)
     │         name · activity · city · description · slogan
     │
     └──► Chromem-go (in-memory vector store)
               local TF-IDF embeddings (512 dimensions, no API key needed)

GET /search?q=... → merge both engines (55% BM25 + 45% semantic) → ranked JSON
```

## Prerequisites

- Go 1.25+
- Internet access for `go mod download` (one-time)

## Setup

```bash
# 1. Download dependencies
go mod download

# 2. Run
go run cmd/congopro-bridge/main.go

# 3. Open http://localhost:8080
```

Set `PORT` env var to change the listen port (default: 8080).

## API

### `GET /search?q=<query>`

Returns up to 30 results sorted by hybrid relevance score.

**Response:**
```json
{
  "query": "restaurant kinshasa",
  "total": 12,
  "results": [
    {
      "id": "5001fe28d964680200000305",
      "name": "DA SAFI DECOR",
      "activity": "Ameublement et mobilier",
      "city": "Kinshasa",
      "country": "Democratic Republic of the Congo",
      "description": "...",
      "score": 0.847
    }
  ]
}
```

### `GET /health`

Returns `{"status":"ready","companies":1534}` once indexing is complete,
or `{"status":"indexing"}` (HTTP 503) while warming up.

## Hybrid scoring

```
score = 0.55 × normalised_bleve_score + 0.45 × normalised_semantic_similarity
```

Both components are normalised to [0,1] before merging.
Results are de-duplicated and sorted descending.

## Embedding approach

No external API is required. A local TF-IDF-style embedding is used:

1. At startup, the top-512 tokens by corpus frequency form the vocabulary.
2. Each document/query is projected onto that vocabulary as a term-frequency vector.
3. Vectors are L2-normalised before storing in Chromem-go.
4. Cosine similarity is computed at query time.

This gives meaningful semantic matching (synonyms, partial matches) while
remaining entirely self-contained and instant to boot.
