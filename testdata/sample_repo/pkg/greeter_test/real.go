// Package greeter_test proves that a legal production import path ending in
// _test cannot collide with GoAST's synthetic external-test package identity.
package greeter_test

// RealPackageSymbol belongs to the real greeter_test import path.
const RealPackageSymbol = true
