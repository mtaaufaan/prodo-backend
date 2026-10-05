ALTER TABLE automation_rule_executions DROP COLUMN IF EXISTS duration_ms;
ALTER TABLE automation_rules DROP COLUMN IF EXISTS template_key;
