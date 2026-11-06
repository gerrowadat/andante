# Proposal: Feed links

Status: proposed, not v1.

A `links` decorator for per-read link templates in `reads.config`:

```json
{"links": [{"label": "Discuss", "url": "https://forum.example/t/{source}-{seq}"}]}
```

`{seq}`, `{title}`, `{url}` and `{source}` are substituted. This is the "discuss it
somewhere else" feature, kept outside andante. It drops in as another `Decorator`.
