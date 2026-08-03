package com.demo.order;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/orders")
public class OrderController {
    private final OrderService orderService;

    public OrderController(OrderService orderService) {
        this.orderService = orderService;
    }

    @PostMapping
    public OrderEntity create(@RequestBody OrderRequest req) {
        return orderService.placeOrder(req.getCustomerId(), req.getAmount());
    }

    @PostMapping("/{id}/cancel")
    public void cancel(@PathVariable String id) {
        orderService.cancel(id);
    }
}
