package e2e

import (
	"context"
	"flag"
	"os"
	"path"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"

	metallbv1alpha1 "github.com/metallb/metallb-operator/api/v1alpha1"
	metallbv1beta1 "github.com/metallb/metallb-operator/api/v1beta1"
	"github.com/metallb/metallb-operator/pkg/status"
	"github.com/metallb/metallb-operator/test/consts"
	testclient "github.com/metallb/metallb-operator/test/e2e/client"
	"github.com/metallb/metallb-operator/test/e2e/k8sreporter"
	"github.com/metallb/metallb-operator/test/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	goclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var autoAssign = false

var UseMetallbResourcesFromFile = false

var OperatorNameSpace = consts.DefaultOperatorNameSpace

var junitPath *string
var reportPath *string

func init() {
	if len(os.Getenv("USE_LOCAL_RESOURCES")) != 0 {
		UseMetallbResourcesFromFile = true
	}

	if ns := os.Getenv("OO_INSTALL_NAMESPACE"); len(ns) != 0 {
		OperatorNameSpace = ns
	}

	junitPath = flag.String("junit", "", "the path for the junit format report")
	reportPath = flag.String("report", "", "the path of the report file containing details for failed tests")
}

func RunE2ETests(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if *junitPath != "" {
		junitFile := path.Join(*junitPath, "e2e_junit.xml")
		rr = append(rr, reporters.NewJUnitReporter(junitFile))
	}

	clients := testclient.New("")

	if *reportPath != "" {
		rr = append(rr, k8sreporter.New(clients, OperatorNameSpace, *reportPath))
	}

	RunSpecsWithDefaultAndCustomReporters(t, "Metallb Operator Validation Suite", rr)
}

var _ = Describe("metallb", func() {
	Context("Creating AddressPool", func() {
		table.DescribeTable("Testing creating addresspool CR successfully", func(addressPoolName string, addresspool *metallbv1alpha1.AddressPool, expectedConfigMap string) {
			By("By creating AddressPool CR")

			Expect(testclient.Client.Create(context.Background(), addresspool)).Should(Succeed())

			key := types.NamespacedName{
				Name:      addressPoolName,
				Namespace: OperatorNameSpace,
			}
			// Create addresspool resource
			By("By checking AddressPool resource is created")
			Eventually(func() error {
				err := testclient.Client.Get(context.Background(), key, addresspool)
				return err
			}, util.Timeout, util.Interval).Should(Succeed())

			// Checking ConfigMap is created
			By("By checking ConfigMap is created match the expected configuration")
			Eventually(func() (string, error) {
				configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				return configmap.Data[consts.MetalLBConfigMapName], err
			}, util.Timeout, util.Interval).Should(MatchYAML(expectedConfigMap))

			By("By checking AddressPool resource and ConfigMap are deleted")
			Eventually(func() bool {
				err := testclient.Client.Delete(context.Background(), addresspool)
				return errors.IsNotFound(err)
			}, util.Timeout, util.Interval).Should(BeTrue(), "Failed to delete AddressPool custom resource")

			Eventually(func() bool {
				_, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
				return errors.IsNotFound(err)
			}, util.Timeout, util.Interval).Should(BeTrue())
		},
			table.Entry("Test AddressPool object with default auto assign", "addresspool1", &metallbv1alpha1.AddressPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addresspool1",
					Namespace: OperatorNameSpace,
				},
				Spec: metallbv1alpha1.AddressPoolSpec{
					Protocol: "layer2",
					Addresses: []string{
						"1.1.1.1",
						"1.1.1.100",
					},
				},
			}, `address-pools:
- name: addresspool1
  protocol: layer2
  addresses:

  - 1.1.1.1
  - 1.1.1.100

`),
			table.Entry("Test AddressPool object with auto assign set to false", "addresspool2", &metallbv1alpha1.AddressPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addresspool2",
					Namespace: OperatorNameSpace,
				},
				Spec: metallbv1alpha1.AddressPoolSpec{
					Protocol: "layer2",
					Addresses: []string{
						"2.2.2.1",
						"2.2.2.100",
					},
					AutoAssign: &autoAssign,
				},
			}, `address-pools:
- name: addresspool2
  protocol: layer2
  auto-assign: false
  addresses:

  - 2.2.2.1
  - 2.2.2.100

`))
	})
	Context("MetalLB contains incorrect data", func() {
		Context("MetalLB has incorrect name", func() {

			var metallb *metallbv1beta1.MetalLB
			BeforeEach(func() {
				var err error
				metallb, err = util.GetMetalLB(OperatorNameSpace, UseMetallbResourcesFromFile)
				Expect(err).ToNot(HaveOccurred())
				metallb.SetName("incorrectname")
				Expect(testclient.Client.Create(context.Background(), metallb)).Should(Succeed())
			})

			AfterEach(func() {
				util.DeleteMetalLB(metallb)
			})
			It("should not be reconciled", func() {
				By("checking MetalLB resource status", func() {
					Eventually(func() bool {
						instance := &metallbv1beta1.MetalLB{}
						err := testclient.Client.Get(context.TODO(), goclient.ObjectKey{Namespace: metallb.Namespace, Name: metallb.Name}, instance)
						Expect(err).ToNot(HaveOccurred())
						for _, condition := range instance.Status.Conditions {
							if condition.Type == status.ConditionDegraded && condition.Status == metav1.ConditionTrue {
								return true
							}
						}
						return false
					}, 30*time.Second, 5*time.Second).Should(BeTrue())
				})
			})
		})

		Context("Correct and incorrect MetalLB resources coexist", func() {
			var correct_metallb *metallbv1beta1.MetalLB
			var incorrect_metallb *metallbv1beta1.MetalLB
			BeforeEach(func() {
				var err error
				correct_metallb, err = util.GetMetalLB(OperatorNameSpace, UseMetallbResourcesFromFile)
				Expect(err).ToNot(HaveOccurred())
				Expect(testclient.Client.Create(context.Background(), correct_metallb)).Should(Succeed())

				incorrect_metallb, err = util.GetMetalLB(OperatorNameSpace, UseMetallbResourcesFromFile)
				Expect(err).ToNot(HaveOccurred())
				incorrect_metallb.SetName("incorrectname")
				Expect(testclient.Client.Create(context.Background(), incorrect_metallb)).Should(Succeed())
			})

			AfterEach(func() {
				util.DeleteMetalLB(incorrect_metallb)
				util.DeleteMetalLB(correct_metallb)
			})
			It("should have correct statuses", func() {
				By("checking MetalLB resource status", func() {
					Eventually(func() bool {
						instance := &metallbv1beta1.MetalLB{}
						err := testclient.Client.Get(context.TODO(), goclient.ObjectKey{Namespace: incorrect_metallb.Namespace, Name: incorrect_metallb.Name}, instance)
						Expect(err).ToNot(HaveOccurred())
						return util.CheckConditionStatus(instance) == status.ConditionDegraded
					}, 30*time.Second, 5*time.Second).Should(BeTrue())

					Eventually(func() bool {
						instance := &metallbv1beta1.MetalLB{}
						err := testclient.Client.Get(context.TODO(), goclient.ObjectKey{Namespace: correct_metallb.Namespace, Name: correct_metallb.Name}, instance)
						Expect(err).ToNot(HaveOccurred())
						return util.CheckConditionStatus(instance) == status.ConditionAvailable
					}, 30*time.Second, 5*time.Second).Should(BeTrue())

					// Delete incorrectly named resource
					err := testclient.Client.Delete(context.Background(), incorrect_metallb)
					Expect(err).ToNot(HaveOccurred())
					Eventually(func() bool {
						err := testclient.Client.Get(context.Background(), goclient.ObjectKey{Namespace: incorrect_metallb.Namespace, Name: incorrect_metallb.Name}, incorrect_metallb)
						return errors.IsNotFound(err)
					}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "Failed to delete MetalLB custom resource")

					// Correctly named resource status should not change
					Eventually(func() bool {
						instance := &metallbv1beta1.MetalLB{}
						err := testclient.Client.Get(context.TODO(), goclient.ObjectKey{Namespace: correct_metallb.Namespace, Name: correct_metallb.Name}, instance)
						Expect(err).ToNot(HaveOccurred())
						return util.CheckConditionStatus(instance) == status.ConditionAvailable
					}, 30*time.Second, 5*time.Second).Should(BeTrue())
				})
			})
		})
	})
	Context("Testing create/delete Multiple AddressPools", func() {
		It("should have created, merged and deleted resources correctly", func() {
			By("Creating first addresspool object ", func() {
				addresspool := &metallbv1alpha1.AddressPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addresspool1",
						Namespace: OperatorNameSpace,
					},
					Spec: metallbv1alpha1.AddressPoolSpec{
						Protocol: "layer2",
						Addresses: []string{
							"1.1.1.1",
							"1.1.1.100",
						},
					},
				}

				Expect(testclient.Client.Create(context.Background(), addresspool)).Should(Succeed())

				key := types.NamespacedName{
					Name:      "addresspool1",
					Namespace: OperatorNameSpace,
				}
				// Create addresspool resource
				By("By checking AddressPool1 resource is created")
				Eventually(func() error {
					err := testclient.Client.Get(context.Background(), key, addresspool)
					return err
				}, util.Timeout, util.Interval).Should(Succeed())

				// Checking ConfigMap is created
				By("By checking ConfigMap is created and matches addresspool1 configuration")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`address-pools:
- name: addresspool1
  protocol: layer2
  addresses:

  - 1.1.1.1
  - 1.1.1.100

`))

			})

			By("Creating second addresspool object ", func() {
				addresspool := &metallbv1alpha1.AddressPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addresspool2",
						Namespace: OperatorNameSpace,
					},
					Spec: metallbv1alpha1.AddressPoolSpec{
						Protocol: "layer2",
						Addresses: []string{
							"2.2.2.1",
							"2.2.2.100",
						},
						AutoAssign: &autoAssign,
					},
				}

				Expect(testclient.Client.Create(context.Background(), addresspool)).Should(Succeed())

				key := types.NamespacedName{
					Name:      "addresspool2",
					Namespace: OperatorNameSpace,
				}
				// Create addresspool resource
				By("By checking AddressPool2 resource is created")
				Eventually(func() error {
					err := testclient.Client.Get(context.Background(), key, addresspool)
					return err
				}, util.Timeout, util.Interval).Should(Succeed())

				// Checking ConfigMap is created
				By("By checking ConfigMap is created and matches addresspool2 configuration")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`address-pools:
- name: addresspool1
  protocol: layer2
  addresses:

  - 1.1.1.1
  - 1.1.1.100

- name: addresspool2
  protocol: layer2
  auto-assign: false
  addresses:

  - 2.2.2.1
  - 2.2.2.100

`))
			})

			By("Deleting the first addresspool object", func() {
				addresspool := &metallbv1alpha1.AddressPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addresspool1",
						Namespace: OperatorNameSpace,
					},
					Spec: metallbv1alpha1.AddressPoolSpec{
						Protocol: "layer2",
						Addresses: []string{
							"1.1.1.1",
							"1.1.1.100",
						},
					},
				}
				Eventually(func() bool {
					err := testclient.Client.Delete(context.Background(), addresspool)
					return errors.IsNotFound(err)
				}, util.Timeout, util.Interval).Should(BeTrue(), "Failed to delete AddressPool custom resource")

				By("By checking ConfigMap matches the expected configuration")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						// if its notfound means that was the last addresspool and configmap is deleted
						if errors.IsNotFound(err) {
							return "", nil
						}
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`address-pools:
- name: addresspool2
  protocol: layer2
  auto-assign: false
  addresses:

  - 2.2.2.1
  - 2.2.2.100

`))

			})

			By("Deleting the second addresspool object", func() {
				addresspool := &metallbv1alpha1.AddressPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addresspool2",
						Namespace: OperatorNameSpace,
					},
					Spec: metallbv1alpha1.AddressPoolSpec{
						Protocol: "layer2",
						Addresses: []string{
							"2.2.2.1",
							"2.2.2.100",
						},
					},
				}
				Eventually(func() bool {
					err := testclient.Client.Delete(context.Background(), addresspool)
					return errors.IsNotFound(err)
				}, util.Timeout, util.Interval).Should(BeTrue(), "Failed to delete AddressPool custom resource")

				By("By checking ConfigMap matches the expected configuration")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						// if its notfound means that was the last addresspool and configmap is deleted
						if errors.IsNotFound(err) {
							return "", nil
						}
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`
`))

			})

			// Make sure Configmap is deleted at the end of this test
			By("By checking ConfigMap is deleted at the end of the test")
			Eventually(func() bool {
				_, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
				return errors.IsNotFound(err)
			}, util.Timeout, util.Interval).Should(BeTrue())
		})
	})

	Context("Testing Update AddressPool", func() {
		It("should have created, update and finally delete addresspool correctly", func() {
			By("Creating addresspool object ", func() {
				addresspool := &metallbv1alpha1.AddressPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addresspool1",
						Namespace: OperatorNameSpace,
					},
					Spec: metallbv1alpha1.AddressPoolSpec{
						Protocol: "layer2",
						Addresses: []string{
							"1.1.1.1",
							"1.1.1.100",
						},
					},
				}

				Expect(testclient.Client.Create(context.Background(), addresspool)).Should(Succeed())

				key := types.NamespacedName{
					Name:      "addresspool1",
					Namespace: OperatorNameSpace,
				}
				// Create addresspool resource
				By("By checking AddressPool resource is created")
				Eventually(func() error {
					err := testclient.Client.Get(context.Background(), key, addresspool)
					return err
				}, util.Timeout, util.Interval).Should(Succeed())

				// Checking ConfigMap is created
				By("By checking ConfigMap is created and matches addresspool configuration")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`address-pools:
- name: addresspool1
  protocol: layer2
  addresses:

  - 1.1.1.1
  - 1.1.1.100

`))

			})

			By("Update the same addresspool object with different range ", func() {
				addresspool := &metallbv1alpha1.AddressPool{}
				key := types.NamespacedName{
					Name:      "addresspool1",
					Namespace: OperatorNameSpace,
				}
				Eventually(func() error {
					err := testclient.Client.Get(context.Background(), key, addresspool)
					return err
				}, util.Timeout, util.Interval).Should(Succeed())

				addresspool.Spec = metallbv1alpha1.AddressPoolSpec{
					Protocol: "layer2",
					Addresses: []string{
						"1.1.1.1",
						"1.1.1.200",
					},
					AutoAssign: &autoAssign,
				}

				Eventually(func() error {
					err := testclient.Client.Update(context.Background(), addresspool)
					return err
				}, util.Timeout, util.Interval).Should(Succeed())

				// Checking ConfigMap is updated
				By("By checking ConfigMap is created and matches updated configuration")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`address-pools:
- name: addresspool1
  protocol: layer2
  auto-assign: false
  addresses:

  - 1.1.1.1
  - 1.1.1.200

`))
			})

			By("Deleting the addresspool object", func() {
				addresspool := &metallbv1alpha1.AddressPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addresspool1",
						Namespace: OperatorNameSpace,
					},
					Spec: metallbv1alpha1.AddressPoolSpec{
						Protocol: "layer2",
						Addresses: []string{
							"1.1.1.1",
							"1.1.1.200",
						},
					},
				}
				Eventually(func() bool {
					err := testclient.Client.Delete(context.Background(), addresspool)
					return errors.IsNotFound(err)
				}, util.Timeout, util.Interval).Should(BeTrue(), "Failed to delete AddressPool custom resource")

				By("Checking ConfigMap is deleted")
				Eventually(func() (string, error) {
					configmap, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
					if err != nil {
						// if its notfound means that was the last addresspool and configmap is deleted
						if errors.IsNotFound(err) {
							return "", nil
						}
						return "", err
					}
					return configmap.Data[consts.MetalLBConfigMapName], err
				}, util.Timeout, util.Interval).Should(MatchYAML(`
`))

			})

			// Make sure Configmap is deleted at the end of this test
			By("Checking ConfigMap is deleted at the end of the test")
			Eventually(func() bool {
				_, err := testclient.Client.ConfigMaps(OperatorNameSpace).Get(context.Background(), consts.MetalLBConfigMapName, metav1.GetOptions{})
				return errors.IsNotFound(err)
			}, util.Timeout, util.Interval).Should(BeTrue())
		})
	})
})
