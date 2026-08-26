-- Models are configured PER AGENT from now on, not in user settings. Preserve
-- behavior for existing users: copy the old settings-level chat model into each
-- of their agents that had no override of its own; after this the app stops
-- reading the settings hint for chat runs.
UPDATE agents
   SET model = COALESCE((SELECT json_extract(us.model_hints, '$.chat')
                           FROM user_settings us WHERE us.user_id = agents.user_id), '')
 WHERE model = ''
   AND COALESCE((SELECT json_extract(us.model_hints, '$.chat')
                   FROM user_settings us WHERE us.user_id = agents.user_id), '') != '';
