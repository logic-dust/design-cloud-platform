package license

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/model"
)

// ============================================================================
// FlexLM License Manager Operations (Read-Only)
// ============================================================================

// flexLMStatus checks the FlexLM license server status
func (s *Service) flexLMStatus(ctx context.Context, licInfo *model.LicenseService) (model.LicenseServiceStatus, error) {
	result, err := s.executeAction(ctx, execution.ActionLicenseStatus, payload.LicenseStatus{
		LicensePath: licInfo.Address,
		LmutilPath:  licInfo.LmutilPath,
	}, licInfo.Server)
	if err != nil {
		return model.LicenseServiceStatusUnknown, err
	}

	isServerUp, isVendorDaemonUp := parseFlexServerStatus(result)
	if isServerUp && isVendorDaemonUp { // all UP
		return model.LicenseServiceStatusUp, nil
	}
	return model.LicenseServiceStatusDown, nil
}

// flexLMListFeatures retrieves FlexLM feature information
func (s *Service) flexLMListFeatures(ctx context.Context, licInfo *model.LicenseService) ([]featureInfo, error) {
	result, err := s.executeAction(ctx, execution.ActionLicenseFeatures, payload.LicenseFeatures{
		LicensePath: licInfo.Address,
		LmutilPath:  licInfo.LmutilPath,
	}, licInfo.Server)
	if err != nil {
		return nil, err
	}
	return parseFlexAllStatusOutput(result)
}

// ============================================================================
// FlexLM Command Execution
// ============================================================================

func (s *Service) executeAction(ctx context.Context, actionType execution.ActionName, payload any, targetNode string) (string, error) {
	executionID := fmt.Sprintf("%s-%d", actionType, time.Now().UnixNano())
	req := execution.NewExecuteRequest(actionType, executionID,
		execution.WithTargetNode(targetNode),
		execution.WithPayload(payload),
		execution.WithTimeout(30*time.Second),
		execution.WithNodeAffinity(execution.NodeAffinityLocalPreferred),
	)

	stdout, err := execution.Run(ctx, s.execSvc, req)
	if err != nil {
		return "", execution.HandleError(s.logger, err, "license."+string(actionType))
	}
	return stdout, nil
}
