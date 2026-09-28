-- Kebalikan 20261103090000: repoint tasks.status_id balik ke baris
-- workspace (by name), lalu hapus seluruh baris custom_statuses
-- scope_type='project' (tidak pernah ada sebelum migrasi ini).
UPDATE tasks t
SET status_id = cs_ws.id
FROM custom_statuses cs_proj
JOIN projects p ON p.id = cs_proj.scope_id
JOIN custom_statuses cs_ws ON cs_ws.scope_type = 'workspace' AND cs_ws.scope_id = p.workspace_id AND cs_ws.name = cs_proj.name
WHERE t.status_id = cs_proj.id AND cs_proj.scope_type = 'project';

DELETE FROM custom_statuses WHERE scope_type = 'project';
