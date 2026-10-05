-- Rule Automation: kesesuaian desain "AW Rule Automation.dc.html" (IG-119).
-- template_key: rule dibuat dari template mana (kartu "DIPAKAI n RULE"); NULL = dibuat manual.
-- duration_ms: lama eksekusi action rule (baris log "... · n ms" dan kolom CSV durasi); NULL = baris lama.
ALTER TABLE automation_rules ADD COLUMN template_key VARCHAR(40);
ALTER TABLE automation_rule_executions ADD COLUMN duration_ms INTEGER;
