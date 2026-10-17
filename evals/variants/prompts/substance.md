---
name: substance
description: Split question 2 of 2 (issue #3, S1). Does the chunk carry something the reader would use?
type: noul
---

The state holds a goal and part of one document split into chunks, each with
its own id.{{if .Source}} The document is {{.Source}}.{{end}}
Does chunk "{{.ID}}" itself state something a reader pursuing the goal would
use: a fact, number, name, date, definition, step, command, piece of code,
decision, or line of reasoning? Boilerplate, navigation, tables of contents,
and passages that only name the subject without saying anything about it are
a no. Judge only this one chunk.
