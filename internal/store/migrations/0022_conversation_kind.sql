-- Separate scheduled-task/agent_run runs from the interactive chat thread so a
-- task never reads or pollutes the conversation (and can't merge its reply with a
-- message that fires at the same moment). 'interactive' = the user's chat thread;
-- 'task' = a per-(user,agent) thread used only by isolated scheduled runs.
ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'interactive';
