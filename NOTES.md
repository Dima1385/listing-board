# Notes

For the rule that an account runs one sheet at a time, Claude suggested a
`SELECT` in the start handler that looks for another running sheet before the
draft is updated. I didn't use it. Two starts sent at the same moment can both
pass that check before either one writes. The rule lives in the database
instead, as a partial unique index on `jobs (user_id) WHERE status = 'running'`,
and the handler turns the unique violation into a 409.

Handler tests cover the 409, the limit being per account, and a second start
returning the same `runId`, and `go test -race ./...` passes. I also ran the API
and the worker against a real Postgres and killed the worker with `kill -9`
halfway through a run. After a restart the sheet was still `running`; the
worker waited out the pause, continued from the next pending step and finished
with the same `runId`.
