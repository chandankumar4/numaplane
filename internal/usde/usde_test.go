package usde

import (
	"context"
	"encoding/json"
	"github.com/numaproj/numaplane/internal/common"
	"github.com/numaproj/numaplane/internal/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"log"
	"testing"
	"time"

	"github.com/numaproj/numaplane/internal/controller/config"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	numaflowv1 "github.com/numaproj/numaflow/pkg/apis/numaflow/v1alpha1"
	apiv1 "github.com/numaproj/numaplane/pkg/apis/numaplane/v1alpha1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

const defaultNamespace = "default"

var pipelineSpecSourceRPU = int64(5)
var pipelineSpecSourceDuration = metav1.Duration{Duration: 2 * time.Second}
var defaultPipelineSpec = numaflowv1.PipelineSpec{
	InterStepBufferServiceName: "my-isbsvc",
	Vertices: []numaflowv1.AbstractVertex{
		{
			Name: "in",
			Source: &numaflowv1.Source{
				Generator: &numaflowv1.GeneratorSource{
					RPU:      &pipelineSpecSourceRPU,
					Duration: &pipelineSpecSourceDuration,
				},
			},
		},
		{
			Name: "cat",
			UDF: &numaflowv1.UDF{
				Builtin: &numaflowv1.Function{
					Name: "cat",
				},
			},
		},
		{
			Name: "out",
			Sink: &numaflowv1.Sink{
				AbstractSink: numaflowv1.AbstractSink{
					Log: &numaflowv1.Log{},
				},
			},
		},
	},
	Edges: []numaflowv1.Edge{
		{
			From: "in",
			To:   "cat",
		},
		{
			From: "cat",
			To:   "out",
		},
	},
}

var volSize, _ = apiresource.ParseQuantity("10Mi")
var memLimit, _ = apiresource.ParseQuantity("10Mi")
var newMemLimit, _ = apiresource.ParseQuantity("20Mi")
var defaultISBServiceSpec = numaflowv1.InterStepBufferServiceSpec{
	Redis: nil,
	JetStream: &numaflowv1.JetStreamBufferService{
		Version: "2.9.6",
		Persistence: &numaflowv1.PersistenceStrategy{
			VolumeSize: &volSize,
		},
		ContainerTemplate: &numaflowv1.ContainerTemplate{
			Resources: v1.ResourceRequirements{
				Limits: v1.ResourceList{v1.ResourceMemory: memLimit},
			},
		},
	},
}

func makePipelineDefinition(pipelineSpec numaflowv1.PipelineSpec) unstructured.Unstructured {
	pipelineSpecRaw, _ := json.Marshal(pipelineSpec)

	prs := apiv1.PipelineRolloutSpec{
		Pipeline: apiv1.Pipeline{
			Spec: runtime.RawExtension{
				Raw: pipelineSpecRaw,
			},
		},
	}

	pipelineDef := unstructured.Unstructured{Object: make(map[string]interface{})}
	pipelineDef.SetGroupVersionKind(numaflowv1.PipelineGroupVersionKind)
	pipelineDef.SetName("test-pipeline")
	pipelineDef.SetNamespace(defaultNamespace)
	var pipelineSpecMap map[string]interface{}
	if err := util.StructToStruct(prs.Pipeline.Spec, &pipelineSpecMap); err != nil {
		log.Fatal(err)
	}
	pipelineDef.Object["spec"] = pipelineSpecMap

	return pipelineDef
}

func makeISBServiceDefinition(isbServiceSpec numaflowv1.InterStepBufferServiceSpec) unstructured.Unstructured {
	isbServiceSpecRaw, _ := json.Marshal(isbServiceSpec)

	isbrs := apiv1.ISBServiceRolloutSpec{
		InterStepBufferService: apiv1.InterStepBufferService{
			Spec: runtime.RawExtension{
				Raw: isbServiceSpecRaw,
			},
		},
	}

	isbServiceDef := unstructured.Unstructured{Object: make(map[string]interface{})}
	isbServiceDef.SetGroupVersionKind(numaflowv1.ISBGroupVersionKind)
	isbServiceDef.SetName("test-isbsvc")
	isbServiceDef.SetNamespace(defaultNamespace)
	var isbServiceSpecMap map[string]interface{}
	if err := util.StructToStruct(isbrs.InterStepBufferService.Spec, &isbServiceSpecMap); err != nil {
		log.Fatal(err)
	}
	isbServiceDef.Object["spec"] = isbServiceSpecMap

	return isbServiceDef
}

func Test_ResourceNeedsUpdating(t *testing.T) {
	ctx := context.Background()

	configManager := config.GetConfigManagerInstance()

	pipelineDefn := makePipelineDefinition(defaultPipelineSpec)
	isbServiceDefn := makeISBServiceDefinition(defaultISBServiceSpec)

	testCases := []struct {
		name                  string
		newDefinition         unstructured.Unstructured
		existingDefinition    unstructured.Unstructured
		usdeConfig            config.USDEConfig
		namespaceConfig       *config.NamespaceConfig
		expectedNeedsUpdating bool
		expectedStrategy      apiv1.UpgradeStrategy
	}{
		{
			name: "NoOp: empty pipeline spec excluded paths, and equivalent metadata",
			newDefinition: func() unstructured.Unstructured {
				pipelineDef := *pipelineDefn.DeepCopy()
				pipelineDef.SetAnnotations(map[string]string{"something": "a"})
				pipelineDef.SetLabels(map[string]string{"something": "a"})
				return pipelineDef
			}(),
			existingDefinition: func() unstructured.Unstructured {
				pipelineDef := *pipelineDefn.DeepCopy()
				pipelineDef.SetAnnotations(map[string]string{"something": "a"})
				pipelineDef.SetLabels(map[string]string{"something": "a"})
				return pipelineDef
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: false,
			expectedStrategy:      apiv1.UpgradeStrategyNoOp,
		},
		{
			name:          "empty pipeline spec excluded paths and change interStepBufferServiceName field",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.InterStepBufferServiceName = "changed-isbsvc"
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyPPND,
		},
		{
			name:          "only exclude interStepBufferServiceName field (changed)",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.InterStepBufferServiceName = "changed-isbsvc"
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName"},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyApply,
		},
		{
			name:               "only exclude interStepBufferServiceName field (NOT changed)",
			newDefinition:      *pipelineDefn.DeepCopy(),
			existingDefinition: *pipelineDefn.DeepCopy(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName"},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: false,
			expectedStrategy:      apiv1.UpgradeStrategyNoOp,
		},
		{
			name:          "only exclude interStepBufferServiceName field and change some other field (no user strategy)",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.Vertices[0].Name = "new-vtx-name"
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName"},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyPPND,
		},
		{
			name:          "only exclude interStepBufferServiceName field and change some other field (with invalid user strategy)",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.Vertices[0].Name = "new-vtx-name"
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "invalid"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyPPND,
		},
		{
			name:          "only exclude interStepBufferServiceName field and change some other field (with valid user strategy)",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.Vertices[0].Name = "new-vtx-name"
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyPPND,
		},
		{
			name:          "with changes in array deep map but excluded",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newRPU := int64(10)
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.InterStepBufferServiceName = "changed-isbsvc"
				newPipelineDef.Vertices[0].Source.Generator.RPU = &newRPU
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName", "vertices.source.generator.rpu"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyApply,
		},
		{
			name:          "with changes in array deep map but excluded parent",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newRPU := int64(10)
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.InterStepBufferServiceName = "changed-isbsvc"
				newPipelineDef.Vertices[0].Source.Generator.RPU = &newRPU
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName", "vertices.source.generator"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyApply,
		},
		{
			name:          "with changes in array deep map but one is NOT excluded",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newRPU := int64(10)
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.Vertices[0].Name = "new-vtx-name"
				newPipelineDef.InterStepBufferServiceName = "changed-isbsvc"
				newPipelineDef.Vertices[0].Source.Generator.RPU = &newRPU
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName", "vertices.source.generator.rpu"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyPPND,
		},
		{
			name:          "with changes in array deep map - detect pointer fields",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.Vertices[2].Sink.Log = nil
				newPipelineDef.Vertices[2].Sink.Blackhole = &numaflowv1.Blackhole{}
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"vertices.sink.log"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyPPND,
		},
		{
			name:          "with changes in array deep map - detect pointer fields - parent field is excluded",
			newDefinition: *pipelineDefn.DeepCopy(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineDef := defaultPipelineSpec.DeepCopy()
				newPipelineDef.Vertices[2].Sink.Log = nil
				newPipelineDef.Vertices[2].Sink.Blackhole = &numaflowv1.Blackhole{}
				return makePipelineDefinition(*newPipelineDef)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"vertices"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyApply,
		},
		{
			name:               "excluded paths not found",
			newDefinition:      *pipelineDefn.DeepCopy(),
			existingDefinition: *pipelineDefn.DeepCopy(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{"vertices.source.something"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: false,
			expectedStrategy:      apiv1.UpgradeStrategyNoOp,
		},
		{
			name:          "isb test",
			newDefinition: isbServiceDefn,
			existingDefinition: func() unstructured.Unstructured {
				newISBServiceSpec := defaultISBServiceSpec.DeepCopy()
				newISBServiceSpec.JetStream.ContainerTemplate.Resources.Limits = v1.ResourceList{v1.ResourceMemory: newMemLimit}
				return makeISBServiceDefinition(*newISBServiceSpec)
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:      config.PPNDStrategyID,
				PipelineSpecExcludedPaths:   []string{"vertices.source.something"},
				ISBServiceSpecExcludedPaths: []string{"jetstream.containerTemplate.resources.limits"},
			},
			namespaceConfig:       &config.NamespaceConfig{UpgradeStrategy: "pause-and-drain"},
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyApply,
		},
		{
			name: "test Annotation changes resulting in Direct Apply",
			newDefinition: func() unstructured.Unstructured {
				pipelineDef := *pipelineDefn.DeepCopy()
				pipelineDef.SetAnnotations(map[string]string{"something": "a"})
				return pipelineDef
			}(),
			existingDefinition: func() unstructured.Unstructured {
				pipelineDef := *pipelineDefn.DeepCopy()
				pipelineDef.SetAnnotations(map[string]string{"something": "b"})
				return pipelineDef
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.PPNDStrategyID,
				PipelineSpecExcludedPaths: []string{},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyApply,
		},
		{
			name: "test Annotation change which requires Progressive update, overriding spec change resulting in Direct Apply",
			newDefinition: func() unstructured.Unstructured {
				pipelineDef := *pipelineDefn.DeepCopy()
				pipelineDef.SetAnnotations(map[string]string{common.AnnotationKeyNumaflowInstanceID: "0"})
				pipelineDef.SetLabels(map[string]string{"something": "a"})
				return pipelineDef
			}(),
			existingDefinition: func() unstructured.Unstructured {
				newPipelineSpec := defaultPipelineSpec.DeepCopy()
				newPipelineSpec.InterStepBufferServiceName = "changed-isbsvc"
				pipelineDef := makePipelineDefinition(*newPipelineSpec)
				pipelineDef.SetAnnotations(map[string]string{common.AnnotationKeyNumaflowInstanceID: "1"})
				pipelineDef.SetLabels(map[string]string{"something": "b"})
				return pipelineDef
			}(),
			usdeConfig: config.USDEConfig{
				DefaultUpgradeStrategy:    config.ProgressiveStrategyID,
				PipelineSpecExcludedPaths: []string{"interStepBufferServiceName"},
			},
			namespaceConfig:       nil,
			expectedNeedsUpdating: true,
			expectedStrategy:      apiv1.UpgradeStrategyProgressive,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configManager.UpdateUSDEConfig(tc.usdeConfig)
			if tc.namespaceConfig != nil {
				configManager.UpdateNamespaceConfig(defaultNamespace, *tc.namespaceConfig)
			} else {
				configManager.UnsetNamespaceConfig(defaultNamespace)
			}

			needsUpdating, strategy, err := ResourceNeedsUpdating(ctx, &tc.newDefinition, &tc.existingDefinition)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedNeedsUpdating, needsUpdating)
			assert.Equal(t, tc.expectedStrategy, strategy)
		})
	}
}

func TestGetMostConservativeStrategy(t *testing.T) {
	tests := []struct {
		name                   string
		strategies             []apiv1.UpgradeStrategy
		expectedStrategyRating int
	}{
		{
			name: "Multiple Strategies",
			strategies: []apiv1.UpgradeStrategy{
				apiv1.UpgradeStrategyNoOp,
				apiv1.UpgradeStrategyApply,
				apiv1.UpgradeStrategyPPND,
			},
			expectedStrategyRating: 2,
		},
		{
			name:                   "Empty List",
			strategies:             []apiv1.UpgradeStrategy{},
			expectedStrategyRating: 0,
		},
		{
			name: "Same Rating",
			strategies: []apiv1.UpgradeStrategy{
				apiv1.UpgradeStrategyPPND,
				apiv1.UpgradeStrategyProgressive,
			},
			expectedStrategyRating: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMostConservativeStrategy(tt.strategies)
			assert.Equal(t, tt.expectedStrategyRating, strategyRating[result])
		})
	}
}
