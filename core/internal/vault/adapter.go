package vault

import "allocation-core/internal/engine"

func engineVerify(path string) (any, error) { return engine.VerifyEvidenceStream(path) }
