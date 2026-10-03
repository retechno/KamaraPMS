# 08. Next features

Ideas that were left out on purpose, with why and what they would touch. A request starts the work (see `README.md`);
this list only keeps them from being forgotten. Move an item to the README status when it is built.

## Rooms and availability

- **Bed counts per room type** (for example 1 King or 2 Twin in one room type) and **bed type as a sellable variant** with
  its own stock or rate. Today a bed type is a description: availability is counted per room type, and a room with
  another bed than the one asked for can still be assigned (see "Bed types" in the README). Touches the availability
  engine (inventory per type and bed), the search, the rate grid and the calendar. Decide first whether a variant
  has its own price or only its own stock.

## Language

- **Numbers in the Indonesian field errors.** The generic texts leave out the limits the server hint carries ("at
  most 100 characters"). Needs interpolation per error code from the backend.
- **The confirmation e-mail** is sent by the server in English; it would take a language per guest or per property.
