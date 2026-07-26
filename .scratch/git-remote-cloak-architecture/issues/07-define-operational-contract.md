# Define the credential, recovery, and failure contract

Type: grilling
Blocked by: 03, 05

## Question

What exact v1 operational contract makes Cloak safe enough and convenient for both a person and a rebooting service?

Specify `init` and `clone`, environment/file credential inputs and precedence, secret masking and validation, fail-closed behavior, local cache lifecycle, concurrent writers, interrupted push/fetch recovery, rollback and corruption diagnosis, full re-cloak rotation, provider rejection handling, and LFS rejection. Keep ordinary Git CLI behavior as the service-facing interface.
