package com.example.client;

import org.springframework.http.HttpMethod;
import org.springframework.web.client.RestTemplate;

// v4: RestTemplate call sites -> client route facts. Each call has a leading-slash
// (internal) path literal, so the extractor emits role=client, framework=resttemplate
// routes keyed by HTTP method.
public class InventoryClient {

    private RestTemplate restTemplate;
    private String baseURL;

    public void readItem(String id) {
        restTemplate.getForEntity(baseURL + "/api/inventory/items/{id}", Item.class, id);
    }

    public void save(Object body) {
        restTemplate.exchange(
            baseURL + "/api/inventory/items",
            HttpMethod.POST,
            body,
            Item.class);
    }

    public void remove(String id) {
        restTemplate.delete("/api/inventory/items/{id}", id);
    }
}
