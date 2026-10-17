---
name: criteria
description: A short question, with the yes and no boundaries in Noul criteria (issue #3, S3).
type: noul
criteria:
  true: >-
    The chunk itself carries substance that bears on the goal: facts,
    numbers, definitions, code, configuration, commands, steps, decisions,
    error messages, or reasoning the reader would otherwise have to go read.
  false: >-
    The chunk is boilerplate (navigation, license headers, cookie or
    newsletter notices, ads, link lists, tables of contents), generic
    background the reader already knows, a passage about a different topic,
    or only mentions a keyword from the goal without saying anything useful
    about it.
---

The state holds a goal and part of one document split into chunks, each with
its own id.{{if .Source}} The document is {{.Source}}.{{end}}
Would chunk "{{.ID}}" help someone accomplish the goal? Judge only this one
chunk.
