package cachecov

// TestCacheVersionCoverage is the regression guard for Enola's core promise: every
// cacheVersion bump documented in internal/engine/cache.go must be backed by a real
// test. cache.go's changelog (the `// vN:` lines) is the spec for the extractors'
// deterministic, multi-language behavior; this guard makes it impossible to add a
// new vN — and therefore ship an extractor behavior change — without registering a
// covering test here.
//
// The guard fails (before CI, if wired into a git pre-push hook) when any of:
//   - a `// vN:` line exists in cache.go with no entry in versionCoverage,
//   - versionCoverage names a test function that does not exist in the tree,
//   - the changelog is not the contiguous range v2..cacheVersion,
//   - versionCoverage has a stale entry for a vN cache.go no longer documents.
//
// When you bump cacheVersion and add a `// vN+1:` line, add a matching entry below
// pointing at the test(s) that assert the new behavior. If you rename a referenced
// test, update its name here — that coupling is intentional: it keeps the map honest.
//
// This package is deliberately dependency-free (no engine/extractor imports, no
// tree-sitter), so it builds and runs in milliseconds.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// versionCoverage maps each cacheVersion (the integer N in "vN") to one or more
// test functions that assert the behavior that version introduced. v1 is the
// implicit baseline (no changelog line), so coverage starts at v2.
var versionCoverage = map[int][]string{
	2:  {"TestExtractURLSessionFacts"},                                                     // Swift URLSession precision
	3:  {"TestExtractFile_FastAPIRoute_Get"},                                               // Python route Name shape
	4:  {"TestRestTemplate_ClientCalls", "TestFeignClient_AndControllerDiscriminated"},     // Java RestTemplate/@FeignClient
	5:  {"TestLaravelRoutes_Resource", "TestPHPHTTPClient_GuzzleRequest"},                   // PHP HTTP client + route DSLs
	6:  {"TestExtractFile_BareConstantReferences"},                                          // Ruby bare-constant RelCalls
	7:  {"TestExtractFile_BuiltinConstantsSkipped"},                                         // Ruby builtin-constant skip + serializer fold
	8:  {"TestCRegistrationMacro", "TestCRegistrationMacroMultiArg"},                        // C/C++ file-scope registration macros
	9:  {"TestCFuncPtrFieldAssignment", "TestCMacroBodyCall"},                               // C/C++ func-ptr fields + macro-body refs
	10: {"TestCStaticRegistrationMacro"},                                                    // C/C++ qualifier-prefixed reg macro
	11: {"TestCCompoundLiteralAssignment"},                                                  // C/C++ in-body compound-literal init
	12: {"TestCMacroBodyValuePosition"},                                                     // C/C++ macro-body value-position func ptrs
	13: {"TestCMacroExpansionTokenPaste"},                                                   // C/C++ token-paste macro expansion
	14: {"TestCStaticSingleArgAttr", "TestCDefineShowAttribute"},                            // C/C++ single-arg DEVICE_ATTR, all-ident scan
	15: {"TestCMachineDescCleanErrorRegion"},                                                // C/C++ clean ERROR-node machine_desc salvage
	16: {"TestCMachineDescErrorRegion"},                                                     // C/C++ assignment/field_expression fragment salvage
	17: {"TestCMachineDescSalvageSkipsFunctionBodies"},                                      // C/C++ full-tree salvage skips function bodies
	18: {"TestExtractTestRefsAST", "TestExtractFile_CustomClassMacroRecorded"},              // Ruby custom macros + KindTestRef
	19: {"TestExtractFile_ClassBodyQualifiedCall"},                                          // Ruby class-body calls + KindFileRef
	20: {"TestExtractFile_TopLevelAssignmentRHS"},                                           // Ruby per-scope pass (assignment RHS)
	21: {"TestExtractFile_InterpolatedSymbolPrefix"},                                        // Ruby interpolated-symbol prefix
	22: {"TestExtractFile_SuperReferencesAncestor", "TestExtractFile_LiteralSymbolDispatch"},// Ruby super + literal-symbol dispatch
	23: {"TestExtractFile_ChainedNoArgCall"},                                                // Ruby chained-receiver no-arg call
	24: {"TestExtractFile_DefaultParamCall", "TestExtractFile_PredicateBangSingleLevelCall"},// Ruby default-param + predicate/bang calls
	25: {"TestExtractFile_DelegateFold"},                                                    // Ruby delegate :a, to: X fold
	26: {"TestExtractFile_LocalRelationScopeCall"},                                          // Ruby scope-like call on identifier receiver
	27: {"TestExtractFile_IvarUnderscoredCall", "TestExtractFile_GvarUnderscoredCall"},      // Ruby @ivar/@@cvar/$gvar receivers
	28: {"TestExtractFile_KlassReceiverDispatch", "TestExtractFile_ClazzKlazzReceiverDispatch"}, // Ruby klass/clazz/klazz dispatch
	29: {"TestExtractFile_InterpolatedStringPrefix"},                                        // Ruby interpolated-string prefix
	30: {"TestExtractFile_StringPrefixGatedOnDispatcher"},                                   // Ruby string-prefix dispatcher gating
	31: {"TestRbComplexity_BlockParamNotCall", "TestRbComplexity_InBatchesNotElementLoop"},  // Ruby block params + find/in_batches
	32: {"TestRbComplexity_SuperIsNotRecursion", "TestRbComplexity_DelegationIsNotRecursion"}, // Ruby super/decorator not recursion
	33: {"TestRbComplexity_SelfClassSiblingIsNotRecursion", "TestRbComplexity_ConstSelfClassMethodIsRecursion"}, // Ruby same-object recursion gating
	34: {"TestRbComplexity_ConstantBoundLoopNoDepth"},                                       // Ruby constant-bounded loops
	35: {"TestRbComplexity_LiteralChainLoopIsBounded"},                                      // Ruby bounded-loop chain unwrap
	36: {"TestAST_ModuleAbstractness", "TestAST_ConcernDetection"},                          // Ruby module abstract prop
	37: {"TestParseXcodeGenProject", "TestManifest_TargetDependencyEdges"},                  // Swift SPM/XcodeGen target modules
	38: {"TestModuleResolver_SharedSourceRootCollapses"},                                    // Swift shared-source-root collapse
	39: {"TestSymbolKind_MethodVsFunc", "TestRelCalls_SelfOptionalChaining"},                // Swift SymbolMethod + member-call edges
	40: {"TestFileScope_TopLevelCallsEmitFileRef", "TestOperator_CustomInfixTracked"},       // Swift file-scope refs + custom operators
	41: {"TestOperator_StandardMulticharNotTracked"},                                        // Swift stdlib-operator exclusion
	42: {"TestExtract_FlattenedTypeMethodResolves", "TestExtensionPropertyGetterCalls"},     // Swift funcIndex fallback + extension property owner
	43: {"TestClassPropertyInitAttachesToType"},                                             // Swift class-property init attaches to type
	44: {"TestSwComplexity_BoundedForRange", "TestSwComplexity_ComputedPropertyGetter"},     // Swift bounded loops + property metrics
	45: {"TestSwComplexity_SubscriptOnSameNameLocalNotRecursion"},                           // Swift subscript-not-call
	46: {"TestSwComplexity_OverloadDelegationNotRecursion"},                                 // Swift label-aware recursion
	47: {"TestSwIO_DirectNetworkPrimitiveSetsIODirect", "TestComputePerformsIO_TransitiveThroughAmbiguousEdge"}, // Swift io_direct/performs_io
	48: {"TestResolveInheritedCalls_SubclassBaseMethod"},                                    // Swift inherited-method resolution
	49: {"TestTargetPriorityAndRoles"},                                                      // Swift test-bundle module + module_role
	50: {"TestTargetPriorityAndRoles"},                                                      // shared ModuleRoleForPath heuristic
	51: {"TestExtract_NoFalseCrossTargetCycle"},                                             // Swift no false module cycle
	52: {"TestAST_MemberFunctionIsMethodKind", "TestAST_OverrideAndDIProviderProps"},        // Kotlin SymbolMethod + nav edges + di/override
	53: {"TestDetectBasePackage_GroovyAndKotlinDSL"},                                        // Kotlin Groovy namespace base package
	54: {"TestAST_CallsOutsideFunctionBody"},                                                // Kotlin default-param + ctor-delegation calls
	55: {"TestAST_CallableReference", "TestAST_QualifiedCallableReference"},                 // Kotlin ::foo / Type::foo callable refs
	56: {"TestKtComplexity_OverloadDelegationNotRecursion", "TestKtComplexity_ReactiveChainNotLoop"}, // Kotlin arity recursion + RxJava/Flow
	57: {"TestKtComplexity_SuperDelegationNotRecursion", "TestKtComplexity_RetrofitMethodPerformsIO"}, // Kotlin super-delegation + Retrofit/Room io
	58: {"TestBuildPackageIndex_MultiModuleSamePrefix", "TestAST_SealedClassIsAbstract"},    // Kotlin/Java multi-module import + sealed abstract
	59: {"TestBuildPackageIndex_MainWinsOverVariant"},                                       // Kotlin main-source-set preference
	60: {"TestModuleRole", "TestDagger_DIvsSpringComponent"},                                // compound test module + Dagger di_component/module
	61: {"TestExtract_FileRef_JSXComponentUsage", "TestExtract_DynamicAndRequireDependencyEdges"}, // TS JSX/require file refs + dep edges
	62: {"TestExtract_FileRef_SameModuleUsePositions"},                                      // TS same-module use positions
	63: {"TestExtract_AnonymousDefaultExport_NamedByFile"},                                  // TS default import -> default export
	64: {"TestExtract_ThisMemberReference_EventHandler"},                                    // TS this.member reference
	65: {"TestExtract_SkipsMinifiedBundle", "TestIsMinifiedSource"},                         // TS minified-file skip
	66: {"TestTsIO_DirectPrimitiveSetsIODirect", "TestTsIO_PerformsIOPropagatesToCaller"},   // TS io_direct/performs_io
	67: {"TestTsIO_NamedImportsFromNetworkModuleNotIO"},                                     // TS tightened io_direct
	68: {"TestExtract_AbstractClass"},                                                       // TS abstract class prop
	69: {"TestExtractURLSessionFacts_TestSourcesSkipped", "TestExtractEndpointFacts"},       // Swift endpoint-enum + test-source skip
	70: {"TestExtractEndpointFacts_DefaultPrefix"},                                          // Swift endpoint version-prefix
	71: {"TestExtractStoredMethodEndpointFacts"},                                            // Swift stored-method endpoints
	72: {"TestWrapperEndpoint_PathAndVerbFromCallSite", "TestRoutes_NestedSingularResource"},// Swift request-wrapper + Ruby nested resources
	73: {"TestExtract_ServerRoutesPerRPC", "TestGRPCClient_OnlyCalledMethodsEmitted"},       // gRPC proto server routes + TS gRPC-web client routes
	74: {"TestGoGRPCClient_EmitsClientRoutes"},                                              // Go gRPC client call-site routes
}

func TestCacheVersionCoverage(t *testing.T) {
	root := repoRoot(t)

	current, changelog := parseCacheVersions(t, filepath.Join(root, "internal", "engine", "cache.go"))

	// 1. Structural: the changelog must be exactly the contiguous range v2..current.
	if current < 2 {
		t.Fatalf("cacheVersion = v%d, want >= v2", current)
	}
	for v := 2; v <= current; v++ {
		if !changelog[v] {
			t.Errorf("cache.go has no `// v%d:` changelog line, but cacheVersion is v%d — every version must be documented", v, current)
		}
	}
	for v := range changelog {
		if v > current {
			t.Errorf("cache.go documents v%d but cacheVersion is only v%d — bump cacheVersion or remove the changelog line", v, current)
		}
	}

	// 2. Every documented version must have a coverage entry.
	for v := range changelog {
		tests, ok := versionCoverage[v]
		if !ok || len(tests) == 0 {
			t.Errorf("v%d is documented in cache.go but has no entry in versionCoverage — register the test(s) that assert it", v)
		}
	}

	// 3. No stale coverage entries for versions cache.go no longer documents.
	for v := range versionCoverage {
		if !changelog[v] {
			t.Errorf("versionCoverage has a stale entry for v%d, which cache.go does not document", v)
		}
	}

	// 4. Every referenced test must actually exist in the tree.
	existing := testFuncNames(t, root)
	for v, tests := range versionCoverage {
		for _, name := range tests {
			if !existing[name] {
				t.Errorf("v%d references test %q, which does not exist — fix the name in versionCoverage (renamed?) or add the test", v, name)
			}
		}
	}
}

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the test working directory")
		}
		dir = parent
	}
}

var (
	cacheVersionConst = regexp.MustCompile(`cacheVersion\s*=\s*"v(\d+)"`)
	changelogLine     = regexp.MustCompile(`^//\s*v(\d+):`)
)

// parseCacheVersions returns the current cacheVersion integer and the set of
// version integers documented by `// vN:` lines in cache.go.
func parseCacheVersions(t *testing.T, path string) (current int, changelog map[int]bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache.go: %v", err)
	}

	changelog = map[int]bool{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if m := changelogLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			changelog[n] = true
			continue
		}
		if m := cacheVersionConst.FindStringSubmatch(line); m != nil {
			current, _ = strconv.Atoi(m[1])
		}
	}
	if current == 0 {
		t.Fatal("could not find the cacheVersion constant in cache.go")
	}
	return current, changelog
}

// testFuncNames scans every *_test.go under internal/ and pkg/ and returns the set
// of top-level `func TestXxx(` names.
func testFuncNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	funcRe := regexp.MustCompile(`^func (Test[A-Za-z0-9_]+)\(`)
	names := map[string]bool{}
	for _, sub := range []string{"internal", "pkg"} {
		base := filepath.Join(root, sub)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(data), "\n") {
				if m := funcRe.FindStringSubmatch(line); m != nil {
					names[m[1]] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
	if len(names) == 0 {
		t.Fatal("found no test functions — scan path is wrong")
	}
	return names
}
