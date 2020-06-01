package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	v1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	"github.com/scytem/i2p-ingress-controller/i2p"
)

const (
	annotationName = "kubernetes.io/ingress.class"
)

type I2pController struct {
	indexer  cache.Indexer
	queue    workqueue.RateLimitingInterface
	informer cache.Controller

	clientset *kubernetes.Clientset

	i2pCfg i2p.I2pConfiguration
	i2p    i2p.I2p
}

func NewI2pController(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller, clientset *kubernetes.Clientset) *I2pController {
	return &I2pController{
		informer:  informer,
		indexer:   indexer,
		queue:     queue,
		clientset: clientset,
		i2pCfg:    i2p.NewI2pConfiguration(),
	}
}

func (i *I2pController) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := i.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two ingresses with the same key are never processed in
	// parallel.
	defer i.queue.Done(key)

	// Invoke the method containing the business logic
	err := i.syncI2p(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	i.handleErr(err, key)
	return true
}

func (i *I2pController) isI2pIngress(ing *v1beta1.Ingress) bool {
	if class, exists := ing.Annotations[annotationName]; exists {
		return class == "i2p"
	}
	return false
}

// syncI2p updates the i2p config with the current set of ingresses
func (i *I2pController) syncI2p(key string) error {
	obj, exists, err := i.indexer.GetByKey(key)
	if err != nil {
		log.Printf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		fmt.Printf("Ingress %s does not exist anymore\n", key)
		i.i2pCfg.RemoveService(key)
		fmt.Println(i.i2pCfg.GetConfiguration())
		i.i2pCfg.SaveConfiguration()
		i.i2p.Reload()
	} else {
		switch o := obj.(type) {
		case *v1beta1.Ingress:
			// Note that you also have to check the uid if you have a local controlled resource, which
			// is dependent on the actual instance, to detect that a Ingress was recreated with the same name
			fmt.Printf("Sync/Add/Update for Ingress %s, namespace %s\n", o.GetName(), o.GetNamespace())

			if !i.isI2pIngress(o) {
				fmt.Println("this isn't a i2p ingress")
				return nil
			}

			backend := o.Spec.Backend
			if backend == nil {
				fmt.Println("sorry, only basic backend supported")
			} else {
				service, err := i.clientset.CoreV1().Services(o.GetNamespace()).Get(backend.ServiceName, meta_v1.GetOptions{})
				if err != nil {
					return err
				}

				clusterIP := service.Spec.ClusterIP

				s := i.i2pCfg.AddService(
					o.GetName(),
					backend.ServiceName,
					o.GetNamespace(),
					clusterIP,
					int(backend.ServicePort.IntVal),
					int(backend.ServicePort.IntVal),
				)

				fmt.Println(i.i2pCfg.GetConfiguration())
				i.i2pCfg.SaveConfiguration()
				i.i2p.Reload()

				time.Sleep(5 * time.Second)

				fmt.Println("finding i2p hostname")
				hostname, err := s.FindHostname()
				if err != nil {
					return err
				}

				fmt.Println("hostname found! ", hostname)

				ingressClient := i.clientset.ExtensionsV1beta1().Ingresses(o.Namespace)

				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					o.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{Hostname: hostname}}

					_, err := ingressClient.UpdateStatus(o)
					return err
				})
				if retryErr != nil {
					panic(fmt.Errorf("Update failed: %v", retryErr))
				}
			}
		}
	}
	return nil
}

func (i *I2pController) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		i.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if i.queue.NumRequeues(key) < 5 {
		log.Printf("Error syncing ingress %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		i.queue.AddRateLimited(key)
		return
	}

	i.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	log.Printf("Dropping ingress %q out of the queue: %v", key, err)
}

func (i *I2pController) runWorker() {
	for i.processNextItem() {
	}
}

func (i *I2pController) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer i.queue.ShutDown()
	log.Println("Starting i2p controller")

	go i.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, i.informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for n := 0; n < threadiness; n++ {
		go wait.Until(i.runWorker, time.Second, stopCh)
	}

	<-stopCh
	log.Println("Stopping i2p controller")
}

func main() {
	var kubeconfig string
	var master string

	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", "", "master url")
	flag.Parse()

	// creates the connection
	config, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		log.Fatal(err)
	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}

	// create the ingress watcher
	ingressListWatcher := cache.NewListWatchFromClient(clientset.ExtensionsV1beta1().RESTClient(), "ingresses", v1.NamespaceAll, fields.Everything())

	// create the workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the ingress key is added to the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Ingress than the version which was responsible for triggering the update.
	indexer, informer := cache.NewIndexerInformer(ingressListWatcher, &v1beta1.Ingress{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := NewI2pController(queue, indexer, informer, clientset)

	controller.i2p.Start()

	// Now let's start the controller
	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(1, stop)

	// Wait forever
	select {}
}
