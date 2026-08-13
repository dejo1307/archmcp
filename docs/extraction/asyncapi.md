# AsyncAPI — event contracts as architecture

AsyncAPI 2.x and 3.x YAML/JSON documents are detected by their top-level
`asyncapi` key. The extractor walks these formats directly because repository
configuration normally excludes YAML and JSON from the source walker.

| Contract construct | Enola fact |
|---|---|
| AsyncAPI 2.x `publish` / 3.x `send` | `storage` topic with `messaging_role: producer` |
| AsyncAPI 2.x `subscribe` / 3.x `receive` | `storage` topic with `messaging_role: consumer` |
| Kafka server protocol | `messaging: kafka` or `kafka-secure`, eligible for Kafka cross-repo linking |

Each operation carries its operation ID, action, message name, content type,
summary, description, tags, specification path, and AsyncAPI version when those
values are present. Local channel, server, message, payload, property, and array
item `$ref` values are resolved across YAML/JSON files. Parameterized addresses
such as `orders/{orderId}` remain intact.

Referenced message payloads remain lightweight: the operation records their
resolved identity in `message_schema`, plus `schema_format` and `content_type`
when present. Enola deliberately does not emit schema fields as symbols or facts
until a compatibility or impact feature can consume them; this keeps schema-only
data out of symbol-based explainers and avoids snapshot noise.

These are messaging topics rather than HTTP routes, so they do not enter HTTP
matching or unused-route findings. Kafka consumer topics participate in Enola's
existing topic-owner linking convention, making asynchronous service dependencies
visible in traversal and impact analysis. Other protocols remain queryable contract
facts until a protocol-specific cross-repository signal is available.

`kafka-secure` remains recorded verbatim on the fact but is classified as Kafka
for linking; its suffix describes authentication or transport security rather
than a different messaging technology.

## Binding contracts to Go Kafka code

AsyncAPI and code extractors share one normalized messaging-operation contract:
`messaging`, `messaging_role`, and `messaging_operation`. AsyncAPI 3.x `send` and
`receive` normalize to `publish` and `subscribe`, matching the vocabulary emitted
from code call sites.

Go files importing a Kafka client package are scanned for conservative literal or
locally-resolved topic calls such as `Publish`, `Subscribe`, Sarama-style
`ConsumePartition`/`SendMessage`, and kafka-go `WriteMessages` with a `Topic`
field. Each call records its enclosing `code_symbol`. A post-link binder joins an
exact topic+operation pair to a unique AsyncAPI operation in the same repository:

- The code fact gains `messaging_contract_bound`, the operation ID, and spec file.
- The contract gains `messaging_implementation_count`, `messaging_implemented_by`,
  and an `implemented_by` relation to the Go symbol.
- Multiple matching contract operations are ambiguous and remain unbound.

The Kafka-import gate is required: ordinary in-process event buses frequently use
methods named `Publish` and `Subscribe`, and treating those as broker operations
would manufacture contract coverage that does not exist.

Remote URL `$ref` documents and broker-specific binding interpretation are not
expanded. Local references are confined to the repository root, and cycles or
missing targets are skipped without dropping the surrounding channel operation.
Schema-field extraction, compatibility, and impact verdicts remain a separate
capability and should ship together.
