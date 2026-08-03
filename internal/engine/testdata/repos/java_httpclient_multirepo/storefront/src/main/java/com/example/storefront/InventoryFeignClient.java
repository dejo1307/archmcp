package com.example.storefront;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.GetMapping;

// A @FeignClient interface pointed at the inventory service. Emits a client route
// with source="feign" — the SECOND hand-written Java client source, and the second
// one the linker's private set used to omit.
@FeignClient(name = "inventory")
public interface InventoryFeignClient {

    @GetMapping("/api/inventory/items")
    Object list();
}
