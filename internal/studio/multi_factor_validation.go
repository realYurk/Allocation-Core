package studio

import (
    "allocationcore/internal/engine"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
)

// sha256Hex is a byte-for-byte configuration precondition, not an amount hash.
func sha256Hex(raw []byte) string {
    sum := sha256.Sum256(raw)
    return hex.EncodeToString(sum[:])
}

// validateEffectiveFactorPlan validates the complete effective allocation
// plan before writing any per-account factor overrides. It does not assert
// Driver eligibility or reference-system equivalence.
func validateEffectiveFactorPlan(raw []byte, rows []factorRow) error {
    spec, err := engine.ParseSpec(raw)
    if err != nil { return fmt.Errorf("规则模板格式错误：%w", err) }
    for _, row := range rows {
        found := false
        for si := range spec.Stages {
            stage := &spec.Stages[si]
            if stage.ID != row.Stage { continue }
            for ai := range stage.Allocations {
                factor := &stage.Allocations[ai]
                if factor.ID != row.Allocation { continue }
                if row.DriverDataset != "" {factor.DriverDataset = row.DriverDataset}
                if row.WeightField != "" {factor.WeightField = row.WeightField}
                if row.Share != "" {factor.Share = row.Share}
                found = true
                break
            }
        }
        if !found { return fmt.Errorf("阶段 %s 找不到因子 %s", row.Stage, row.Allocation) }
    }
    // Sum exact six-place fixed-point fractions as the engine does.
    for _, stage := range spec.Stages {
        if !engine.Enabled(stage.Enabled) || (stage.Operation != "" && stage.Operation != "ALLOCATE") { continue }
        total := int64(0)
        for _, factor := range stage.Allocations {
            if !engine.Enabled(factor.Enabled) { continue }
            share := factor.Share
            if share == "" { share = "1" }
            n, err := engine.ParseScaled(share, 6)
            if err != nil || n < 0 || n > 1000000 {
                return fmt.Errorf("阶段 %s 的因子 %s 比例无效：%q", stage.ID, factor.ID, share)
            }
            total += n
        }
        if total != 1000000 {
            return fmt.Errorf("阶段「%s」的分摊比例合计为 %.4f%%，必须恰好 100%%，未保存任何变更", stage.ID, float64(total)/10000)
        }
    }
    if _, err := engine.Compile(spec); err != nil {
        return fmt.Errorf("最终生效规则未通过计算引擎校验：%w", err)
    }
    return nil
}
