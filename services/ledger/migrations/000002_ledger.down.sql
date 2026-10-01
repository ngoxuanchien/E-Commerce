DROP TABLE IF EXISTS outbox, idempotency_keys, processed_events, pending_postings, payouts,
    escrow_allocations, entries, journal_transactions, accounts;
DROP FUNCTION IF EXISTS ledger_check_liability_not_in_debit(), ledger_check_transaction_balanced(), ledger_reject_mutation();
