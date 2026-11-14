# Routes

| route | method | handler | perms |
|---|---|---|---|
| /api/audit/search | DELETE | h09 | audit.read |
| /api/audit/v28/{id}/notes | DELETE | h41 | audit.read, billing.write |
| /api/audit/v63/search | DELETE | h33 | audit.read, webhooks.manage |
| /api/audit/v89/{id} | DELETE | h25 | audit.read |
| /api/audit/{id} | DELETE | h01 | audit.read |
| /api/audit/{id}/notes | DELETE | h17 | audit.read |
| /api/billing | GET | h12 | billing.read, invoices.read |
| /api/billing/export | GET | h04 | billing.read |
| /api/billing/v22 | GET | h36 | billing.read, billing.write |
| /api/billing/v7/{id}/history | GET | h44 | billing.read, billing.write |
| /api/billing/v99/export | GET | h28 | billing.read, billing.write |
| /api/billing/{id}/history | GET | h20 | billing.read |
| /api/invoices/search | DELETE | h21 | invoices.read, invoices.write |
| /api/invoices/v1/{id}/notes | DELETE | h29 | invoices.read |
| /api/invoices/v38/{id} | DELETE | h37 | invoices.read, invoices.write |
| /api/invoices/{id} | DELETE | h13 | invoices.read, invoices.write |
| /api/invoices/{id}/notes | DELETE | h05 | invoices.write |
| /api/invoices/{id}/notes | DELETE | h31 | reports.export |
| /api/orders | GET | h24 | orders.read, orders.write |
| /api/orders/export | GET | h16 | orders.read, orders.refund |
| /api/orders/v12/{id}/history | GET | h32 | orders.write |
| /api/orders/v33/export | GET | h40 | orders.read |
| /api/orders/v94 | GET | h48 | orders.read, orders.refund |
| /api/orders/{id}/history | GET | h08 | orders.refund |
| /api/reports/search | POST | h15 | reports.export, reports.read |
| /api/reports/v66/{id}/notes | POST | h47 | reports.read |
| /api/reports/v85/search | POST | h39 | reports.read |
| /api/reports/{id} | POST | h07 | orders.write, reports.export |
| /api/reports/{id}/notes | POST | h23 | reports.read |
| /api/settings/search | POST | h03 | settings.write |
| /api/settings/v45/search | POST | h27 | settings.read, settings.write |
| /api/settings/v75/{id} | POST | h43 | settings.write |
| /api/settings/v96/{id}/notes | POST | h35 | settings.read, settings.write |
| /api/settings/{id} | POST | h19 | settings.write, users.admin |
| /api/settings/{id}/notes | POST | h11 | settings.read, settings.write |
| /api/users | PUT | h18 | users.read |
| /api/users | PUT | h45 | invoices.read, invoices.write |
| /api/users/export | PUT | h10 | users.admin, users.write |
| /api/users/v17/export | PUT | h34 | users.admin, users.read |
| /api/users/v18 | PUT | h42 | users.write |
| /api/users/v35/{id}/history | PUT | h26 | reports.export, users.admin |
| /api/users/{id}/history | PUT | h02 | users.read, users.write |
| /api/webhooks | PUT | h06 | webhooks.manage |
| /api/webhooks/export | PUT | h22 | webhooks.manage |
| /api/webhooks/v64/export | PUT | h46 | webhooks.manage |
| /api/webhooks/v85 | PUT | h30 | webhooks.manage |
| /api/webhooks/v86/{id}/history | PUT | h38 | webhooks.manage |
| /api/webhooks/{id}/history | PUT | h14 | webhooks.manage |

## Conflicts

DELETE /api/invoices/{id}/notes: h05, h31
PUT /api/users: h18, h45

END-OF-ROUTES
