package com.example.storefront;

import org.springframework.http.HttpMethod;
import org.springframework.web.client.RestTemplate;

// RestTemplate call sites into the inventory service. These emit client routes with
// source="java-http-client", which the cross-repo linker must classify as a
// HAND-WRITTEN call site (via="http-client"), not as an edge implied by a spec.
public class InventoryRestClient {

    private RestTemplate restTemplate;
    private String baseURL;

    public void read(String id) {
        restTemplate.getForEntity(baseURL + "/api/inventory/items/{id}", Object.class, id);
    }

    public void create(Object body) {
        restTemplate.exchange(
            baseURL + "/api/inventory/items",
            HttpMethod.POST,
            body,
            Object.class);
    }
}
