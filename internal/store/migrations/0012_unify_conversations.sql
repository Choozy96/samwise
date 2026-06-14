-- Unify conversations across channels: one thread per (user, agent).
--
-- Previously a conversation was keyed by (user_id, channel, agent_id), so Web and
-- Telegram each had their own transcript and rolling summary — they didn't sync.
-- We now key by (user_id, agent_id) and tag each message with the channel it came
-- from. This migration merges any existing per-channel threads for the same
-- user+agent into one canonical conversation so no history is orphaned.

-- 1. Record each message's source channel (so a unified view can show origin).
ALTER TABLE messages ADD COLUMN channel TEXT NOT NULL DEFAULT 'web';
UPDATE messages
   SET channel = COALESCE(
       (SELECT c.channel FROM conversations c WHERE c.id = messages.conversation_id),
       'web');

-- 2. Map every conversation to the canonical (lowest-id) one for its
--    (user_id, agent_id) group.
CREATE TEMP TABLE conv_map AS
SELECT c.id AS conv_id,
       (SELECT MIN(c2.id) FROM conversations c2
         WHERE c2.user_id = c.user_id
           AND COALESCE(c2.agent_id, 0) = COALESCE(c.agent_id, 0)) AS canon_id
  FROM conversations c;

-- 3. Repoint transcripts and run history onto the canonical conversation.
UPDATE messages
   SET conversation_id = (SELECT canon_id FROM conv_map WHERE conv_id = messages.conversation_id)
 WHERE conversation_id IN (SELECT conv_id FROM conv_map WHERE conv_id <> canon_id);

UPDATE runs
   SET conversation_id = (SELECT canon_id FROM conv_map WHERE conv_id = runs.conversation_id)
 WHERE conversation_id IN (SELECT conv_id FROM conv_map WHERE conv_id <> canon_id);

-- 4. A merged thread's old summary / native session no longer matches the combined
--    transcript — clear them so they rebuild cleanly on the next run.
UPDATE conversations
   SET summary = '', summary_msg_count = 0, harness_session_id = NULL
 WHERE id IN (SELECT DISTINCT canon_id FROM conv_map WHERE conv_id <> canon_id);

-- 5. Delete the now-empty sibling conversations.
DELETE FROM conversations
 WHERE id IN (SELECT conv_id FROM conv_map WHERE conv_id <> canon_id);

DROP TABLE conv_map;

-- 6. Replace the per-channel index with one matching the new lookup key.
DROP INDEX IF EXISTS idx_conversations_user;
CREATE INDEX idx_conversations_user_agent ON conversations(user_id, agent_id);
