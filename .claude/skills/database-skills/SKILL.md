---
name: database-skills
description: Portable database and data-systems skill pack for SQL, key/value stores, caching, and message brokers.
---

# Database Skills

## Use when
Use this skill for SQL, schema design, key/value stores, caching, message brokers, replication, migrations, and data modeling.

## Goals
- Choose the right storage or messaging pattern
- Design for correctness, performance, and operability
- Explain tradeoffs clearly
- Produce practical schemas, queries, configs, or code

## Rules
- Prefer simple, reliable designs.
- For SQL, check schema, indexes, query plans, transactions, and isolation.
- For key/value systems, consider TTLs, hot keys, sharding, and consistency.
- For caches, consider eviction policy, invalidation, and read/write strategy.
- For brokers, cover ordering, retries, backpressure, and delivery guarantees.
- Mention failure modes, scaling limits, and operational costs.
- If requirements are unclear, make a reasonable assumption and proceed.

## Coverage
### Relational databases
- SQL querying
- Schema design
- Normalization
- Indexing
- Query optimization
- Transactions
- Isolation levels
- Locking and concurrency
- Migrations
- Backup and restore

### Key/Value stores
- Data modeling for key/value access
- TTL and expiration
- Sharding and partitioning
- Replication
- Consistency tradeoffs
- Hot key mitigation

### Caching
- LRU cache design
- LFU cache design
- Cache invalidation
- Read-through caching
- Write-through caching
- Write-back caching
- Distributed caching

### Message brokers
- Pub/sub messaging
- Durable queues
- Consumer groups
- Ordering guarantees
- Retries and dead-letter queues
- Backpressure
- At-least-once delivery
- Exactly-once tradeoffs

### Data system operations
- Replication and failover
- High availability
- Disaster recovery
- Schema evolution
- Observability and monitoring
- Capacity planning
- Performance troubleshooting

## Output format
When answering, use this order:
1. Short diagnosis
2. Recommended design
3. Example schema, query, config, or code
4. Risks and tradeoffs

## Examples
### Example 1: SQL performance
- Check the query plan
- Add or adjust indexes
- Reduce full scans
- Verify transaction boundaries

### Example 2: Cache design
- Pick an eviction policy
- Define invalidation rules
- Decide read-through vs write-through
- Plan for cache stampedes

### Example 3: Broker usage
- Define message ordering needs
- Choose retry and DLQ behavior
- Set consumer group strategy
- Explain delivery semantics

## Do not
- Do not assume one database fits all problems.
- Do not ignore failure recovery.
- Do not hide tradeoffs.
- Do not overcomplicate simple systems.
