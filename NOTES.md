# Atomic ledger operations

`Store.Award` owns payment deduplication, the monthly cap and insertion as one atomic operation. `Store.Reverse` inserts at most one reversal using the original award's member, month and actual points. Awards of zero points are retained for deduplication; reversals never reduce monthly cap usage. Processing failures are nacked; successful operations and unknown-payment reversals are acked, with unknown payments logged.

The local `MemoryStore` uses its mutex for these operations. Simulated latency occurs before locking, so different members' round trips overlap. Handlers sharing the store share its guarantees; independent stores do not. The mutex is only the local implementation of the storage contract.

## PostgreSQL implementation (transaction steps)

Use one shared database and `READ COMMITTED` transactions. Make `ledger_entries.payment_id` and `kind` NOT NULL with `UNIQUE (payment_id, kind)`. Keep ledger entries immutable. Add a persistent guard table `ledger_members (member_id text PRIMARY KEY)`; do not delete guard rows while processing payments.

For `Award`: (1) `BEGIN`; (2) `INSERT INTO ledger_members (member_id) VALUES ($1) ON CONFLICT DO NOTHING`; (3) `SELECT member_id FROM ledger_members WHERE member_id = $1 FOR UPDATE`; (4) check for an award by payment ID and return success if it exists; (5) sum only award points for the member and the settlement's UTC calendar month; (6) compute `min(requested_points, max(monthly_cap - used, 0))`; (7) insert the award with `ON CONFLICT (payment_id, kind) DO NOTHING`; (8) `COMMIT` before reporting success. A conflicting insert makes no other ledger changes.

For `Reverse`: (1) `BEGIN`; (2) find the original award by payment ID; if absent, end the transaction and return `ErrUnknownPayment`; (3) lock its member's guard row with `SELECT ... FOR UPDATE`; (4) re-read the award and check for an existing reversal; (5) if not already reversed, insert a reversal copying the original member and month and negating its stored points, using `ON CONFLICT (payment_id, kind) DO NOTHING`; (6) `COMMIT` before reporting success. Reversals do not subtract from the sum of awards used for the cap.

Every production writer must follow this protocol. The member guard exists before locking: selecting a missing payment FOR UPDATE would lock no row. Locks last until transaction end and coordinate all replicas; different members lock different rows. Subsequent READ COMMITTED queries see preceding committed awards after a lock wait. Uniqueness also prevents duplicate entries if inconsistent events name different members for the same payment.

Roll back on errors; retry whole transactions on deadlocks or serialization failures with bounded backoff, then return an error for Nack if retries are exhausted. If a commit's outcome is unknown, return an error for redelivery: an already committed operation is safe to repeat. Ack only after confirmed commit; a crash between commit and Ack also results in harmless redelivery.

Added tests cover concurrent reversals and cap preservation after reversals, including zero-point awards and repeated settlements. Existing tests cover concurrent awards, caps, transient failures, validation and parallel processing. PostgreSQL steps above are a design, not an implemented or integration-tested adapter.
