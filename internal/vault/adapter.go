package vault

import "allocationcore/internal/engine"

func engineVerify(path string) (any, error) { return engine.VerifyEvidenceStream(path) }
