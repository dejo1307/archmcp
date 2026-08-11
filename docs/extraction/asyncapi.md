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
values are present. AsyncAPI 3.x local channel and server `$ref` values are
resolved. Parameterized addresses such as `orders/{orderId}` remain intact.

These are messaging topics rather than HTTP routes, so they do not enter HTTP
matching or unused-route findings. Kafka consumer topics participate in Enola's
existing topic-owner linking convention, making asynchronous service dependencies
visible in traversal and impact analysis. Other protocols remain queryable contract
facts until a protocol-specific cross-repository signal is available.

`kafka-secure` remains recorded verbatim on the fact but is classified as Kafka
for linking; its suffix describes authentication or transport security rather
than a different messaging technology.

External `$ref` documents and broker-specific binding interpretation are not
expanded. Message payload schemas are recorded by message identity and content
type; schema-field impact analysis is a separate capability.
