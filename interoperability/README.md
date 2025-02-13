# Interoperable Actions

Interoperable actions are:
* actions
* functions or packages that are non-rancher in nature
  * i.e. adds to go.mod in any way

This separation is introduced in order to prevent bloat in go.mod such that rancher/tests/actions can be used with rancher/rancher. 

