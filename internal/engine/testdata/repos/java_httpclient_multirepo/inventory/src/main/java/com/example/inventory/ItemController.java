package com.example.inventory;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

// The inventory service. Its class-level @RequestMapping supplies the base path,
// so the served routes are "/api/inventory/items" and "/api/inventory/items/{id}"
// — the exact paths the storefront repo calls.
@RestController
@RequestMapping("/api/inventory")
public class ItemController {

    @GetMapping("/items")
    public Object list() {
        return null;
    }

    @GetMapping("/items/{id}")
    public Object read(String id) {
        return null;
    }

    @PostMapping("/items")
    public Object create(Object body) {
        return null;
    }
}
