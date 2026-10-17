---
name: nosource
description: The shipped 0.1.102 prompt without the source sentence (issue #3, S4).
type: noul
---

The state holds a goal and part of one document split into chunks, each with
its own id.
Would chunk "{{.ID}}" help someone accomplish the goal?

Answer yes if the chunk itself carries substance that bears on the goal:
facts, numbers, definitions, code, configuration, commands, steps, decisions,
error messages, or reasoning the reader would otherwise have to go read.
Answer no for boilerplate (navigation, license headers, cookie or newsletter
notices, ads, link lists, tables of contents), generic background the reader
already knows, and passages about a different topic. A chunk that only
mentions a keyword from the goal without saying anything useful about it is
a no. Judge only this one chunk.
