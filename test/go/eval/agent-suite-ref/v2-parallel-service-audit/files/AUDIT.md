# Service audit

## Port conflicts

7004: auth-worker, ledger-api
7017: catalog-api, ranker-api
7024: email-worker, feed-api
7053: orders-worker, search-worker

## Unknown owners

billing-worker: team-payments
inbox-worker: Team-Core
quota-worker: team-legacy

## Missing dependencies

cart-worker -> fraud-api
media-api -> searchd
tax-api -> ledger-v1

## Dependency cycles

auth-api -> geo-api -> kyc-worker -> ledger-worker -> ranker-worker -> auth-api
auth-api -> geo-api -> orders-worker -> pricing-worker -> kyc-worker -> ledger-worker -> ranker-worker -> auth-api
auth-api -> geo-api -> ranker-worker -> auth-api
auth-api -> kyc-worker -> ledger-worker -> ranker-worker -> auth-api
kyc-worker -> ledger-worker -> orders-worker -> pricing-worker -> kyc-worker
kyc-worker -> ledger-worker -> pricing-worker -> kyc-worker
kyc-worker -> pricing-worker -> kyc-worker
