ALTER TABLE organizations ADD COLUMN contract_end_at TIMESTAMPTZ;

UPDATE organizations o
SET contract_end_at = (
  SELECT gc.end_at FROM group_contracts gc WHERE gc.group_id = o.group_id ORDER BY gc.end_at DESC LIMIT 1
);

DROP TABLE group_contracts;
