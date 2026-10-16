---
name: subject
description: Split question 1 of 2 (issue #3, S1). Is the chunk on the goal's subject?
type: noul
---

The state holds a goal and part of one document split into chunks, each with
its own id.{{if .Source}} The document is {{.Source}}.{{end}}
Is chunk "{{.ID}}" about the subject the goal asks about: the same topic,
system, people, events, or material, however briefly or deeply it covers it?
Judge only this one chunk.
