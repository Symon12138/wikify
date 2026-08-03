package com.demo.order;

public class OrderEntity {
    private String id;
    private String customerId;
    private long amount;
    private String status;

    public OrderEntity(String customerId, long amount, String status) {
        this.customerId = customerId;
        this.amount = amount;
        this.status = status;
    }
    public String getId() { return id; }
    public void setStatus(String status) { this.status = status; }
}
