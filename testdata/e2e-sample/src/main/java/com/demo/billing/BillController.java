package com.demo.billing;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/bills")
public class BillController {
    private final BillService billService;
    public BillController(BillService billService) { this.billService = billService; }

    @PostMapping("/{id}/settle")
    public void settle(@PathVariable String id) {
        billService.settle(id);
    }
}
