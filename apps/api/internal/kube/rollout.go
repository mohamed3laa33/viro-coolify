package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Rollout health values (the derived RolloutStatus.Health).
const (
	healthComplete    = "complete"
	healthProgressing = "progressing"
	healthDegraded    = "degraded"
	healthScaledZero  = "scaled-zero"
	healthUnknown     = "unknown"
)

// AppRolloutStatus reports the detailed deploy-progress view of the release's
// rollout. It prefers the Deployment (the common app path) and falls back to a
// StatefulSet (databases / stateful services). Every count comes from the
// controller's OBSERVED status — there is no synthesized progress. The derived
// Health/Reason/Message let the UI render an honest progress bar and surface a
// stuck rollout's cause (e.g. ProgressDeadlineExceeded) instead of spinning.
func (b *KubeBackend) AppRolloutStatus(ctx context.Context, namespace, release string) (RolloutStatus, error) {
	if dep, err := b.client.AppsV1().Deployments(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
		return deploymentRollout(dep), nil
	} else if !errors.IsNotFound(err) {
		return RolloutStatus{Health: healthUnknown, Phase: "Unknown"}, err
	}
	if sts, err := b.client.AppsV1().StatefulSets(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
		return statefulSetRollout(sts), nil
	} else if !errors.IsNotFound(err) {
		return RolloutStatus{Health: healthUnknown, Phase: "Unknown"}, err
	}
	return RolloutStatus{Health: healthUnknown, Phase: "Unknown"},
		fmt.Errorf("kube: no Deployment/StatefulSet %q in %q", release, namespace)
}

// deploymentRollout maps a Deployment's spec/status onto a RolloutStatus,
// deriving health from its replica counts and Progressing/ReplicaFailure
// conditions.
func deploymentRollout(dep *appsv1.Deployment) RolloutStatus {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	st := dep.Status
	rs := RolloutStatus{
		Desired:            int(desired),
		Ready:              int(st.ReadyReplicas),
		Updated:            int(st.UpdatedReplicas),
		Available:          int(st.AvailableReplicas),
		ObservedGeneration: st.ObservedGeneration,
		Generation:         dep.Generation,
	}

	// A controller failure (ReplicaFailure=True, or Progressing=False which the
	// Deployment controller sets on ProgressDeadlineExceeded) is degraded — surface
	// its reason/message so the UI shows WHY the deploy is stuck.
	for i := range st.Conditions {
		c := st.Conditions[i]
		if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
			rs.Health = healthDegraded
			rs.Reason, rs.Message = c.Reason, c.Message
		}
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse {
			rs.Health = healthDegraded
			rs.Reason, rs.Message = c.Reason, c.Message
		}
	}

	rs.Phase = rolloutPhase(rs)
	if rs.Health == "" {
		rs.Health = deriveHealth(rs)
	}
	return rs
}

// statefulSetRollout maps a StatefulSet's spec/status onto a RolloutStatus. A
// StatefulSet exposes ReadyReplicas + UpdatedReplicas but NOT AvailableReplicas,
// so Available mirrors Ready (best-effort). StatefulSets have no Progressing
// condition, so health is derived purely from the counts.
func statefulSetRollout(sts *appsv1.StatefulSet) RolloutStatus {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	st := sts.Status
	rs := RolloutStatus{
		Desired:            int(desired),
		Ready:              int(st.ReadyReplicas),
		Updated:            int(st.UpdatedReplicas),
		Available:          int(st.ReadyReplicas), // STS has no AvailableReplicas
		ObservedGeneration: st.ObservedGeneration,
		Generation:         sts.Generation,
	}
	rs.Phase = rolloutPhase(rs)
	rs.Health = deriveHealth(rs)
	return rs
}

// rolloutPhase derives the coarse one-word phase (matching Status.Phase's
// vocabulary) from a RolloutStatus's counts.
func rolloutPhase(rs RolloutStatus) string {
	switch {
	case rs.Desired == 0:
		return "Scaled to zero"
	case rs.Ready < rs.Desired:
		return "Pending"
	default:
		return "Running"
	}
}

// deriveHealth derives the rollout health from the counts when no controller
// condition already marked it degraded. The rollout is COMPLETE only when the
// controller has observed the latest spec (ObservedGeneration>=Generation, when a
// Generation is reported) AND all desired pods are ready AND fully rolled to the
// newest template (Updated>=Desired).
func deriveHealth(rs RolloutStatus) string {
	if rs.Desired == 0 {
		return healthScaledZero
	}
	stale := rs.Generation > 0 && rs.ObservedGeneration < rs.Generation
	if !stale && rs.Ready >= rs.Desired && rs.Updated >= rs.Desired {
		return healthComplete
	}
	return healthProgressing
}
