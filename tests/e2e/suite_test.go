/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"sigs.k8s.io/controller-runtime/pkg/client/config"

	numaflowv1 "github.com/numaproj/numaflow/pkg/apis/numaflow/v1alpha1"
	apiv1 "github.com/numaproj/numaplane/pkg/apis/numaplane/v1alpha1"
	planeversiond "github.com/numaproj/numaplane/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "E2E Suite")
}

var _ = BeforeSuite(func() {

	var err error
	// make output directory to store temporary outputs; if it's there from before delete it
	disableTestArtifacts = os.Getenv("DISABLE_TEST_ARTIFACTS")
	// pod logs env
	enablePodLogs = os.Getenv("ENABLE_POD_LOGS")
	if disableTestArtifacts != "true" {
		setupOutputDir()
	}

	openFiles = make(map[string]*os.File)

	stopCh = make(chan struct{})

	ppnd = os.Getenv("PPND")

	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	ctx, cancel = context.WithTimeout(context.Background(), suiteTimeout) // Note: if we start seeing "client rate limiter: context deadline exceeded", we need to increase this value

	scheme := runtime.NewScheme()
	err = apiv1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = numaflowv1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	useExistingCluster := true

	restConfig := config.GetConfigOrDie()

	testEnv = &envtest.Environment{
		UseExistingCluster:       &useExistingCluster,
		Config:                   restConfig,
		AttachControlPlaneOutput: true,
	}

	cfg, err := testEnv.Start()
	Expect(cfg).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	pipelineRolloutClient = planeversiond.NewForConfigOrDie(cfg).NumaplaneV1alpha1().PipelineRollouts(Namespace)
	Expect(pipelineRolloutClient).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	monoVertexRolloutClient = planeversiond.NewForConfigOrDie(cfg).NumaplaneV1alpha1().MonoVertexRollouts(Namespace)
	Expect(monoVertexRolloutClient).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	isbServiceRolloutClient = planeversiond.NewForConfigOrDie(cfg).NumaplaneV1alpha1().ISBServiceRollouts(Namespace)
	Expect(isbServiceRolloutClient).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	numaflowControllerRolloutClient = planeversiond.NewForConfigOrDie(cfg).NumaplaneV1alpha1().NumaflowControllerRollouts(Namespace)
	Expect(numaflowControllerRolloutClient).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	kubeClient, err = kubernetes.NewForConfig(cfg)
	Expect(kubeClient).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	dynamicClient = *dynamic.NewForConfigOrDie(cfg)
	Expect(dynamicClient).NotTo(BeNil())
	Expect(err).NotTo(HaveOccurred())

	if disableTestArtifacts != "true" {

		wg.Add(1)
		go watchPods()

		wg.Add(1)
		go watchNumaflowControllerRollout()

		startPipelineRolloutWatches()

		startISBServiceRolloutWatches()

		startMonoVertexRolloutWatches()

		if enablePodLogs == "true" {
			wg.Add(1)
			go watchPodLogs(kubeClient, Namespace, NumaplaneLabel)

			wg.Add(1)
			go watchPodLogs(kubeClient, Namespace, NumaflowLabel)
		}

	}

})

var _ = AfterSuite(func() {

	cancel()
	By("tearing down test environment")
	close(stopCh)

	err := closeAllFiles()
	Expect(err).NotTo(HaveOccurred())

	err = testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

})

var _ = AfterEach(func() {

	report := CurrentSpecReport()
	if report.Failed() {
		AbortSuite("Test spec has failed, aborting suite run")
	}

})

func setupOutputDir() {

	var (
		dirs = []string{ResourceChangesPipelineOutputPath, ResourceChangesISBServiceOutputPath,
			ResourceChangesMonoVertexOutputPath, ResourceChangesNumaflowControllerOutputPath}
		logDirs = []string{PodLogsPipelineOutputPath, PodLogsISBServiceOutputPath,
			PodLogsNumaflowControllerOutputPath, PodLogsMonoVertexOutputPath, PodLogsNumaplaneControllerOutputPath}
	)

	// clear out prior runs output files
	directory := "output"
	_, err := os.Stat(directory)
	if err == nil {
		err = os.RemoveAll(directory)
		Expect(err).NotTo(HaveOccurred())
	}

	// output/resources contains `kubectl get` output for each resource
	if disableTestArtifacts != "true" {
		for _, dir := range dirs {
			if dir == ResourceChangesPipelineOutputPath {
				err = os.MkdirAll(filepath.Join(dir, "vertices"), os.ModePerm)
				Expect(err).NotTo(HaveOccurred())
			}
			if dir == ResourceChangesISBServiceOutputPath {
				err = os.MkdirAll(filepath.Join(dir, "statefulsets"), os.ModePerm)
				Expect(err).NotTo(HaveOccurred())
			}
			err = os.MkdirAll(filepath.Join(dir, "pods"), os.ModePerm)
			Expect(err).NotTo(HaveOccurred())
		}

		// output/pods contains pod logs for each resource
		if enablePodLogs == "true" {
			for _, dir := range logDirs {
				err = os.MkdirAll(dir, os.ModePerm)
				Expect(err).NotTo(HaveOccurred())
			}
		}
	}

}
