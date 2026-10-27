//! Interface discovery, neighbour observation and address identity helpers.

mod address_identity;
mod interfaces;
mod neighbors;

pub use address_identity::{ipv4_identity, mac_identity};
pub use interfaces::{discover, discover_from, Interface, InterfaceInfo, RoleRecommendation};
pub use neighbors::{read_arp_table, Neighbor, ARP_TABLE_PATH};
