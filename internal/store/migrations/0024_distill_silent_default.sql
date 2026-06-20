-- Memory distillation is silent by default now: the end-of-day "here's what I
-- remembered" note is opt-in (Settings → Memory). Flip existing users to silent;
-- new users are inserted with distill_notify = 0 (see CreateUser).
UPDATE user_settings SET distill_notify = 0;
