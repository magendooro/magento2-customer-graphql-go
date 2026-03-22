# Resolver: Wire orders into Customer type

## Description

Wire `OrderService` into the GraphQL resolver layer so that `customer { orders(...) { ... } }` queries work correctly with filter, sort, and pagination arguments.

## Acceptance Criteria

- [ ] `graph/resolver.go` — `Resolver` struct has `OrderService *service.OrderService` field
- [ ] `graph/resolver.go` — `NewResolver` creates `OrderRepository` and `OrderService`, adds to returned `Resolver`
- [ ] `graph/resolver.go` — `Customer() CustomerResolver` method returns `&customerResolver{r}`
- [ ] `graph/resolver.go` — `customerResolver` struct embeds `*Resolver`
- [ ] `graph/schema.resolvers.go` — `customerResolver.Orders(ctx, obj, filter, currentPage, pageSize, sort)` implemented
- [ ] `gqlgen.yml` has `Customer.fields.orders.resolver: true` so gqlgen generates the `CustomerResolver` interface
- [ ] Build passes: `GOTOOLCHAIN=auto go build -o server ./cmd/server/`

## Solution Approach

After gqlgen regeneration (which creates the `CustomerResolver` interface in `generated.go`), add the `Customer()` method to `Resolver` in `resolver.go` and implement the generated `Orders` stub in `schema.resolvers.go`. The `obj *model.Customer` parameter provides the customer ID (`strconv.Atoi(obj.ID)`), so no additional auth context lookup is needed in the resolver.

## Labels

`enhancement`, `resolver`, `graphql`
