package com.demo.order;

import org.springframework.stereotype.Repository;

@Repository
public class OrderRepository {
    public void save(OrderEntity e) {}
    public OrderEntity findById(String id) { return null; }
}
