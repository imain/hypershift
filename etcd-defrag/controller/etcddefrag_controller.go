package etcddefrag

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-logr/logr"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/pkg/etcdcli"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	pollWaitDuration                       = 2 * time.Second
	pollTimeoutDuration                    = 60 * time.Second
	maxDefragFailuresBeforeDegrade         = 3
	defaultResync                          = 10 * time.Hour
	defragDegradedCondition                = "DefragControllerDegraded"
	externalPrivateServiceLabel            = "hypershift.openshift.io/external-private-service"
	minDefragBytes                 int64   = 100 * 1024 * 1024 // 100MB
	minDefragWaitDuration                  = 36 * time.Second
	maxFragmentedPercentage        float64 = 45

	controllerRequeueDuration = 1 * time.Minute
)

type DefragController struct {
	client.Client
	log logr.Logger

	ControllerName string
	HCPNamespace   string
	upsert.CreateOrUpdateProvider

	etcdClient         etcdcli.EtcdClient
	numDefragFailures  int
	defragWaitDuration time.Duration
}

/*

NOTES:

Basic setup for an operator goes here.  I have this being called from:

https://github.com/imain/hypershift/blob/etcd-defrag/control-plane-operator/main.go#L379

The idea is to have the 'Reconcile' loop continuously call into itself by returning a requeue
after X time.  I don't think there is a big rush here.. 5 minutes to an hour would be fine.
Obviously for testing purposes shorter is better.

The actual defrag won't take place unless the percentage of defragmentation is above the
given threshold above.  You can use etcdcli in the etcd container to create/remove key/value
pairs and it will eventually become fragmented enough to make the call.

I'm not sure how to handle the default setup, which is a non-HA configuration.  eg 1 etcd container.
In the original they do not perform defragmentation on single etcd configurations because it could
take some time and during that time the API will be down.  Having a quorum of 3 means that if defragmentation
takes a long time, a new leader can be elected while the existing leader performs defragmentation.  I don't
know how big a deal that is in practice however.  Maybe we lower the defrag percentage to keep the time shorter?

Work involved from here:
- Make sure the operator setup actually works and we can requeue properly.
- Make sure the etcdcli works and we can talk to our etcd container(s).
- Verify that the defragmentation actually takes place.
- There is a namespace being used in etcdcli to find the etcd container.  I'm not sure if it's correct for our case.
  I put a TODO note in there.
*/

func (r *DefragController) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).Named(r.ControllerName)

	if _, err := b.Build(r); err != nil {
		return err
	}

	endpointsFunc := func() ([]string, error) {
		return r.etcdEndpoints(ctx)
	}
	r.etcdClient = etcdcli.NewEtcdClient(endpointsFunc, events.NewLoggingEventRecorder(r.ControllerName))

	return nil
}

func (r *DefragController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = ctrl.LoggerFrom(ctx)
	r.log.Info("reconciling for ETCD DEFRAG")

	// Fetch the HostedControlPlane
	hostedControlPlane := &hyperv1.HostedControlPlane{}
	err := r.Client.Get(ctx, req.NamespacedName, hostedControlPlane)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Return early if HostedControlPlane is deleted
	if !hostedControlPlane.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	r.log.Info("ETCD DEFRAG stage 2")

	// TODO: Add logic to check hyperv1 api for correct settings to enable defrag.

	// Run defragmentation.  This checks first to see if it needs it.
	if err := r.runDefrag(ctx); err != nil {
		r.log.Info("ETCD DEFRAG err")
		return ctrl.Result{RequeueAfter: controllerRequeueDuration}, fmt.Errorf("failed to defragment etcd: %w", err)
	}

	r.log.Info("Returning and requeuing after 1min.  ETCD-DEFRAG")

	// Always requeue so that we can check again.
	return ctrl.Result{RequeueAfter: controllerRequeueDuration}, nil
}

func (r *DefragController) etcdEndpoints(ctx context.Context) ([]string, error) {
	var eplist []string

	// Get the endpoints of the etcd service in the HCP namespace.
	endpoints := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "etcd-client", Namespace: r.HCPNamespace}}

	if err := r.Get(ctx, crclient.ObjectKeyFromObject(endpoints), endpoints); err != nil {
		if apierrors.IsNotFound(err) {
			r.log.Info("Operator service endpoints have not been created yet\n")
			return eplist, nil
		}
		return eplist, err
	}
	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		r.log.Info("Waiting for endpoints addresses to be populated\n")
		return eplist, nil
	}

	for _, s := range endpoints.Subsets {
		for i := 0; i < len(s.Addresses); i++ {
			// https correct?
			eplist = append(eplist, fmt.Sprintf("https://%s:%s", s.Addresses[i].IP, s.Ports[i].Port))
		}
	}
	return eplist, nil
}

/*

NOTES:

Everything from here down is from the cluster-etcd-controller code.
It's been modified mostly to replace 'c' with 'r' as the object name.

https://github.com/openshift/cluster-etcd-operator/blob/master/pkg/operator/defragcontroller/defragcontroller.go

I've also removed some checks.  We'll have to check on the local HCP objects to see whether it is appropriate to
run the defragmentation operation or not.  Note that this also uses etcdcli from the same source tree:

https://github.com/openshift/cluster-etcd-operator/tree/master/pkg/etcdcli

This drags in a lot of dependencies but I think they are mostly pretty sane.  I've copied that package
to hypershift/pkg/etcdcli in this patch.

*/

func (r *DefragController) runDefrag(ctx context.Context) error {
	// Do not defrag if any of the cluster members are unhealthy.
	memberHealth, err := r.etcdClient.MemberHealth(ctx)
	if err != nil {
		return err
	}
	if !etcdcli.IsClusterHealthy(memberHealth) {
		return fmt.Errorf("cluster is unhealthy: %s", memberHealth.Status())
	}

	members, err := r.etcdClient.MemberList(ctx)
	if err != nil {
		return err
	}

	// filter out learner members since they don't support the defragment API call
	var etcdMembers []*etcdserverpb.Member
	for _, m := range members {
		if !m.IsLearner {
			etcdMembers = append(etcdMembers, m)
		}
	}

	var endpointStatus []*clientv3.StatusResponse
	var leader *clientv3.StatusResponse
	for _, member := range etcdMembers {
		if len(member.ClientURLs) == 0 {
			// skip unstarted member
			continue
		}
		status, err := r.etcdClient.Status(ctx, member.ClientURLs[0])
		if err != nil {
			return err
		}
		if leader == nil && status.Leader == member.ID {
			leader = status
			continue
		}
		endpointStatus = append(endpointStatus, status)
	}

	// Leader last if possible.
	if leader != nil {
		r.log.Info("Appending leader last, ID: %x", leader.Header.MemberId)
		endpointStatus = append(endpointStatus, leader)
	}

	successfulDefrags := 0
	var errors []error
	for _, status := range endpointStatus {
		member, err := getMemberFromStatus(etcdMembers, status)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		// Check each member's status which includes the db size on disk "DbSize" and the db size in use "DbSizeInUse"
		// compare the % difference and if that difference is over the max diff threshold and also above the minimum
		// db size we defrag the members state file. In the case where this command only partially completed controller
		// can clean that up on the next sync. Having the db sizes slightly different is not a problem in itself.
		if r.isEndpointBackendFragmented(member, status) {
			r.log.Info("DefragControllerDefragmentAttempt: Attempting defrag on member: %s, memberID: %x, dbSize: %d, dbInUse: %d, leader ID: %d", member.Name, member.ID, status.DbSize, status.DbSizeInUse, status.Leader)
			if _, err := r.etcdClient.Defragment(ctx, member); err != nil {
				// Defrag can timeout if defragmentation takes longer than etcdcli.DefragDialTimeout.
				errMsg := fmt.Sprintf("failed defrag on member: %s, memberID: %x: %v", member.Name, member.ID, err)
				r.log.Info("DefragControllerDefragment Failed")
				errors = append(errors, fmt.Errorf(errMsg))
				continue
			}

			r.log.Info("DefragControllerDefragmentSuccess: etcd member has been defragmented: %s, memberID: %d", member.Name, member.ID)
			successfulDefrags++

			// Give cluster time to recover before we move to the next member.
			if err := wait.Poll(
				pollWaitDuration,
				pollTimeoutDuration,
				func() (bool, error) {
					// Ensure defragmentation attempts have clear observable signal.
					r.log.Info("Sleeping to allow cluster to recover before defrag next member: %v", r.defragWaitDuration)
					time.Sleep(r.defragWaitDuration)

					memberHealth, err := r.etcdClient.MemberHealth(ctx)
					if err != nil {
						r.log.Info("failed checking member health: %v", err)
						return false, nil
					}
					if !etcdcli.IsClusterHealthy(memberHealth) {
						klog.Warningf("cluster is unhealthy: %s", memberHealth.Status())
						return false, nil
					}
					return true, nil
				}); err != nil {
				errors = append(errors, fmt.Errorf("timeout waiting for cluster to stabilize after defrag: %w", err))
			}
		} else {
			// no fragmentation needed is also a success
			successfulDefrags++
		}
	}

	if successfulDefrags != len(endpointStatus) {
		r.numDefragFailures++
		r.log.Info("DefragControllerDefragmentPartialFailure: only %d/%d members were successfully defragmented, %d tries left before controller degrades", successfulDefrags, len(endpointStatus), maxDefragFailuresBeforeDegrade-r.numDefragFailures)

		// return all errors here for the sync loop to retry immediately
		return v1helpers.NewMultiLineAggregate(errors)
	}

	if len(errors) > 0 {
		r.log.Info("found errors even though all members have been successfully defragmented: %s",
			v1helpers.NewMultiLineAggregate(errors).Error())
	}

	return nil
}

// isEndpointBackendFragmented checks the status of all cluster members to ensure that no members have a fragmented store.
// This can happen if the operator starts defrag of the cluster but then loses leader status and is rescheduled before
// the operator can defrag all members.
func (r *DefragController) isEndpointBackendFragmented(member *etcdserverpb.Member, endpointStatus *clientv3.StatusResponse) bool {
	if endpointStatus == nil {
		r.log.Info("endpoint status validation failed: %v", endpointStatus)
		return false
	}
	fragmentedPercentage := checkFragmentationPercentage(endpointStatus.DbSize, endpointStatus.DbSizeInUse)
	if fragmentedPercentage > 0.00 {
		r.log.Info("etcd member %q backend store fragmented: %.2f %%, dbSize: %d", member.Name, fragmentedPercentage, endpointStatus.DbSize)
	}
	return fragmentedPercentage >= maxFragmentedPercentage && endpointStatus.DbSize >= minDefragBytes
}

func checkFragmentationPercentage(ondisk, inuse int64) float64 {
	diff := float64(ondisk - inuse)
	fragmentedPercentage := (diff / float64(ondisk)) * 100
	return math.Round(fragmentedPercentage*100) / 100
}

func getMemberFromStatus(members []*etcdserverpb.Member, endpointStatus *clientv3.StatusResponse) (*etcdserverpb.Member, error) {
	if endpointStatus == nil {
		return nil, fmt.Errorf("endpoint status validation failed: %v", endpointStatus)
	}
	for _, member := range members {
		if member.ID == endpointStatus.Header.MemberId {
			return member, nil
		}
	}
	return nil, fmt.Errorf("no member found in MemberList matching ID: %v", endpointStatus.Header.MemberId)
}
