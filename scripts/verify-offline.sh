#!/usr/bin/env bash
# Offline end-to-end verification (no LLM).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FIX="${1:-}"
CLEANUP=0
if [[ -z "$FIX" ]]; then
  FIX="$(mktemp -d -t wikify-e2e-XXXX)"
  CLEANUP=0
  mkdir -p \
    "$FIX/src/main/java/com/demo/order" \
    "$FIX/src/main/java/com/demo/billing" \
    "$FIX/src/main/resources" \
    "$FIX/src/test/java" \
    "$FIX/deploy"
  cat > "$FIX/src/main/java/com/demo/order/OrderController.java" <<'EOF'
package com.demo.order;
public class OrderController {
  public void create() {}
  public void cancel() {}
}
EOF
  cat > "$FIX/src/main/java/com/demo/order/OrderService.java" <<'EOF'
package com.demo.order;
public class OrderService {
  public void placeOrder() {}
}
EOF
  cat > "$FIX/src/main/java/com/demo/order/OrderEntity.java" <<'EOF'
package com.demo.order;
public class OrderEntity { private String id; }
EOF
  cat > "$FIX/src/main/java/com/demo/billing/BillController.java" <<'EOF'
package com.demo.billing;
public class BillController { public void settle() {} }
EOF
  cat > "$FIX/src/main/resources/application.yml" <<'EOF'
server:
  port: 8080
EOF
  cat > "$FIX/Dockerfile" <<'EOF'
FROM eclipse-temurin:17
COPY . /app
EOF
  cat > "$FIX/src/test/java/OrderTest.java" <<'EOF'
class OrderTest {}
EOF
  mkdir -p "$FIX/.wikify"
  cat > "$FIX/.wikify/wiki_plan.yaml" <<'EOF'
version: 1
wiki:
  template: architecture
  notes:
    - text: Focus on order and billing capabilities
      author: e2e
  documents: []
scope:
  include:
    - src/**
    - Dockerfile
  exclude:
    - src/test/**
EOF
fi

echo "fixture: $FIX"
go run ./scripts/e2e_offline.go "$FIX"
echo "fixture kept at: $FIX"
