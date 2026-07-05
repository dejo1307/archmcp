package com.example.client;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.GetMapping;

// v4: a @FeignClient interface -> client route facts. The declared method's
// @GetMapping path becomes a role=client, framework=feign route whose target_hint
// is the FeignClient name ("shipping").
@FeignClient(name = "shipping")
public interface ShippingClient {

    @GetMapping("/api/shipping/{id}")
    Shipment getShipment(Long id);
}
