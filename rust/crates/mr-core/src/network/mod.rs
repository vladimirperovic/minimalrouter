//! Interface discovery, neighbour observation and address identity helpers.

mod address_identity;
mod interfaces;
mod neighbors;

pub use address_identity::{ipv4_identity, mac_identity};
pub use interfaces::{Interface, InterfaceInfo, RoleRecommendation, discover, discover_from};
pub use neighbors::{ARP_TABLE_PATH, Neighbor, read_arp_table};
