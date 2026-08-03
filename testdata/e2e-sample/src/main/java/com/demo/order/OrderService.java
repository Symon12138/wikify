package com.demo.order;

import com.demo.billing.BillService;
import org.springframework.stereotype.Service;

@Service
public class OrderService {
    private final OrderRepository repo;
    private final BillService billService;

    public OrderService(OrderRepository repo, BillService billService) {
        this.repo = repo;
        this.billService = billService;
    }

    public OrderEntity placeOrder(String customerId, long amount) {
        OrderEntity o = new OrderEntity(customerId, amount, "CREATED");
        repo.save(o);
        billService.createBill(o.getId(), amount);
        return o;
    }

    public void cancel(String id) {
        OrderEntity o = repo.findById(id);
        o.setStatus("CANCELLED");
        repo.save(o);
    }
}
