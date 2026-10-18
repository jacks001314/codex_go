# #42344 / #42348 / #42351 / #42353 Windows sandbox service provisioning

## Upstream scope

- `#42344`: managed-policy validation while impersonating the provisioning
  client, plus a cache-bypassing cloud-config load.
- `#42348`: authenticated service-client process identity.
- `#42351`: enable authenticated provisioning over the service IPC host.
- `#42353`: experimental service provisioning, service-hosted proxy listeners,
  and service-specific errors.

## Go architecture

Go has no `windows-sandbox-service` crate/process, Windows service host,
provisioning IPC client, `machine_policy` validator, or
`AuthorizedClientProcess` identity. Windows sandbox setup is performed through
the direct elevated `RunElevatedProvisioningSetup` helper. Prior audits for
`#49067`, `#48829`, `#50480`, and `#50507` recorded the same absence.

There is therefore no provisioning-client policy/cache path, service request
authentication, retained-handle IPC protocol, or service-hosted proxy listener
to modify in Go.

## Conclusion

N/A by architecture for the whole family.
