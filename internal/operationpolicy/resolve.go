package operationpolicy

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/openkratos/api/policy/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

var (
	permissionPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
	classPattern      = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

// Resolved is the validated policy for one unary operation.
type Resolved struct {
	Access           policy.Access
	ValidateRequest  bool
	Audit            bool
	IdempotencyLevel descriptorpb.MethodOptions_IdempotencyLevel
	IdempotencyClass string
	RateClass        string
	BudgetClass      string
	requireAll       []string
	requireAny       []string
}

// RequireAll returns a copy of the permissions that must all be granted.
func (r Resolved) RequireAll() []string { return slices.Clone(r.requireAll) }

// RequireAny returns a copy of the permissions where at least one must be granted.
func (r Resolved) RequireAny() []string { return slices.Clone(r.requireAny) }

// Resolve merges and validates the service and method policy for method.
func Resolve(method protoreflect.MethodDescriptor) (Resolved, error) {
	if method == nil {
		return Resolved{}, fmt.Errorf("operation policy: method descriptor is nil")
	}
	service, ok := method.Parent().(protoreflect.ServiceDescriptor)
	if !ok {
		return Resolved{}, fmt.Errorf("operation policy: %s has no service descriptor", method.FullName())
	}
	operation := "/" + string(service.FullName()) + "/" + string(method.Name())

	serviceOptions, ok := service.Options().(*descriptorpb.ServiceOptions)
	if !ok {
		return Resolved{}, fmt.Errorf("operation policy %s: invalid service options %T", operation, service.Options())
	}
	methodOptions, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok {
		return Resolved{}, fmt.Errorf("operation policy %s: invalid method options %T", operation, method.Options())
	}

	merged := new(policy.OperationPolicy)
	declared := false
	if proto.HasExtension(serviceOptions, policy.E_DefaultPolicy) {
		declared = true
		apply(merged, proto.GetExtension(serviceOptions, policy.E_DefaultPolicy).(*policy.OperationPolicy))
	}
	if proto.HasExtension(methodOptions, policy.E_Policy) {
		declared = true
		apply(merged, proto.GetExtension(methodOptions, policy.E_Policy).(*policy.OperationPolicy))
	}
	if declared && (method.IsStreamingClient() || method.IsStreamingServer()) {
		return Resolved{}, fmt.Errorf("operation policy %s: policy v1 does not support %s streaming", operation, streamingShape(method))
	}

	resolved := Resolved{
		Access:           merged.GetAccess(),
		ValidateRequest:  merged.GetValidateRequest(),
		Audit:            merged.GetAudit(),
		IdempotencyLevel: methodOptions.GetIdempotencyLevel(),
		IdempotencyClass: merged.GetIdempotencyClass(),
		RateClass:        merged.GetRateClass(),
		BudgetClass:      merged.GetBudgetClass(),
	}
	if permissions := merged.GetPermissions(); permissions != nil {
		resolved.requireAll = slices.Clone(permissions.GetRequireAll())
		resolved.requireAny = slices.Clone(permissions.GetRequireAny())
	}
	if err := validate(operation, resolved); err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}

func apply(dst, src *policy.OperationPolicy) {
	if src == nil {
		return
	}
	if src.Access != nil {
		dst.Access = src.Access
	}
	if src.Permissions != nil {
		dst.Permissions = proto.Clone(src.Permissions).(*policy.PermissionPolicy)
	}
	if src.ValidateRequest != nil {
		dst.ValidateRequest = src.ValidateRequest
	}
	if src.Audit != nil {
		dst.Audit = src.Audit
	}
	if src.IdempotencyClass != nil {
		dst.IdempotencyClass = src.IdempotencyClass
	}
	if src.RateClass != nil {
		dst.RateClass = src.RateClass
	}
	if src.BudgetClass != nil {
		dst.BudgetClass = src.BudgetClass
	}
}

func validate(operation string, resolved Resolved) error {
	switch resolved.Access {
	case policy.Access_ACCESS_PUBLIC, policy.Access_ACCESS_AUTHENTICATED:
		if len(resolved.requireAll) > 0 || len(resolved.requireAny) > 0 {
			return fmt.Errorf("operation policy %s: %s access cannot require permissions", operation, resolved.Access)
		}
	case policy.Access_ACCESS_AUTHORIZED:
		if len(resolved.requireAll) == 0 && len(resolved.requireAny) == 0 {
			return fmt.Errorf("operation policy %s: authorized access requires at least one permission", operation)
		}
	case policy.Access_ACCESS_UNSPECIFIED:
		return fmt.Errorf("operation policy %s: access must be explicit", operation)
	default:
		return fmt.Errorf("operation policy %s: unsupported access value %d", operation, resolved.Access)
	}

	seen := make(map[string]string, len(resolved.requireAll)+len(resolved.requireAny))
	if err := validatePermissions(operation, "require_all", resolved.requireAll, seen); err != nil {
		return err
	}
	if err := validatePermissions(operation, "require_any", resolved.requireAny, seen); err != nil {
		return err
	}
	for _, class := range []struct {
		kind  string
		value string
	}{
		{kind: "idempotency_class", value: resolved.IdempotencyClass},
		{kind: "rate_class", value: resolved.RateClass},
		{kind: "budget_class", value: resolved.BudgetClass},
	} {
		if err := validateClass(operation, class.kind, class.value); err != nil {
			return err
		}
	}
	if resolved.IdempotencyClass != "" && resolved.IdempotencyLevel != descriptorpb.MethodOptions_IDEMPOTENT {
		return fmt.Errorf("operation policy %s: idempotency_class requires idempotency_level IDEMPOTENT", operation)
	}
	return nil
}

func validatePermissions(operation, list string, permissions []string, seen map[string]string) error {
	for _, permission := range permissions {
		if strings.TrimSpace(permission) != permission || !permissionPattern.MatchString(permission) {
			return fmt.Errorf("operation policy %s: invalid %s permission %q", operation, list, permission)
		}
		if previous, ok := seen[permission]; ok {
			return fmt.Errorf("operation policy %s: permission %q appears in %s and %s", operation, permission, previous, list)
		}
		seen[permission] = list
	}
	return nil
}

func validateClass(operation, kind, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || !classPattern.MatchString(value) {
		return fmt.Errorf("operation policy %s: invalid %s %q", operation, kind, value)
	}
	return nil
}

func streamingShape(method protoreflect.MethodDescriptor) string {
	switch {
	case method.IsStreamingClient() && method.IsStreamingServer():
		return "bidirectional"
	case method.IsStreamingClient():
		return "client"
	default:
		return "server"
	}
}
