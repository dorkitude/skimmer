---
name: DEFAULT
description: Is this item from a list (search results, records, log lines, ...) worth keeping for the goal? One question per item.
type: noul
---

The state holds a goal and a list of items, each with an id. The items may be
search results, records, file names, log lines, issues, or anything else.
Is item "{{.ID}}" worth keeping for someone pursuing the goal?

Answer yes if the item is relevant to the goal and likely to be useful: it is
about the right subject, satisfies any constraints the goal states, and is
something the reader would want to look at or act on. Answer no if it is off
topic, fails a stated constraint, is a duplicate-looking or empty entry, or
only shares surface keywords with the goal. Judge only this one item.

Goal: {{.Goal}}

Item {{.ID}}:
{{.Text}}
