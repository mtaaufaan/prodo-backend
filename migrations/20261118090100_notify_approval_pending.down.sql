-- S7A-12 (IG-158): batalkan fungsi notifikasi approval_pending.
DROP FUNCTION IF EXISTS prodo_notify_approval_pending(UUID, UUID);
