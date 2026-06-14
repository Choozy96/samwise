package orchestrator

import "context"

// userSecretsEnv decrypts the user's stored secrets into an env map for injection
// into a run. Best-effort: a row that fails to decrypt is skipped (logged) so one
// bad secret can't break every run. Returns nil if the box is disabled or there
// are no secrets.
func (o *Orchestrator) userSecretsEnv(ctx context.Context, userID int64) map[string]string {
	if o.box == nil || !o.box.Enabled() {
		return nil
	}
	secs, err := o.db.ListSecrets(ctx, userID)
	if err != nil {
		o.log.Error("loading user secrets", "user_id", userID, "err", err)
		return nil
	}
	if len(secs) == 0 {
		return nil
	}
	env := make(map[string]string, len(secs))
	for _, s := range secs {
		raw, derr := o.box.Decrypt(s.ValueEnc)
		if derr != nil {
			o.log.Error("decrypting user secret", "name", s.Name, "err", derr)
			continue
		}
		env[s.Name] = string(raw)
	}
	return env
}
