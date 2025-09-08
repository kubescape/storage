package file

// This test is used to populate wlids.json when updating the testdata
//func TestKubernetesAPI_fetchWlidsFromRunningWorkloads(t *testing.T) {
//	client, disco, err := NewKubernetesClient()
//	assert.NoError(t, err)
//	resourceMaps := ResourceMaps{
//		RunningWlidsToContainerNames: new(maps.SafeMap[string, sets.Set[string]]),
//	}
//	bytes, err := os.ReadFile("testdata/wlids.json")
//	assert.NoError(t, err)
//	var existing map[string][]string
//	err = json.Unmarshal(bytes, &existing)
//	assert.NoError(t, err)
//	for k, v := range existing {
//		resourceMaps.RunningWlidsToContainerNames.Set(k, sets.NewSet(v...))
//	}
//	h := NewKubernetesAPI(client, disco)
//	err = h.fetchDataFromWorkloads(&resourceMaps)
//	assert.NoError(t, err)
//	bytes, err = json.Marshal(resourceMaps.RunningWlidsToContainerNames)
//	assert.NoError(t, err)
//	err = os.WriteFile("testdata/wlids.json", bytes, 0644)
//	assert.NoError(t, err)
//}

//func TestKubernetesAPI_FetchResources(t *testing.T) {
//	cfg, err := config.LoadConfig("../../../configuration")
//	require.NoError(t, err)
//	client, err := NewKubernetesClient()
//	require.NoError(t, err)
//	kubernetesAPI := NewKubernetesAPI(cfg, client)
//	namespaces, err := kubernetesAPI.ListNamespaces()
//	require.NoError(t, err)
//	for i, ns := range namespaces {
//		t.Logf("fetching resources from namespace %d/%d: %s", i+1, len(namespaces), ns)
//		_, err := kubernetesAPI.FetchResources(ns)
//		assert.NoError(t, err)
//	}
//}
