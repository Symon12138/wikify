package com.demo.billing;

import org.springframework.stereotype.Service;

@Service
public class BillService {
    public void createBill(String orderId, long amount) {}
    public void settle(String billId) {}
}
